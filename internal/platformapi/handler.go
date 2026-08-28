package platformapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OcheOps/platform-IDP/internal/costs"
	"github.com/OcheOps/platform-IDP/internal/goldenpath"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type Config struct {
	SchemaDirectory, TemplateDirectory, GeneratedDirectory string
	GitHubOrganization, AWSRegion, GitHubActionsRoleARN    string
	Authenticate                                           func(*http.Request) (string, error)
	Authorize                                              func(actor, owner string) error
	CostPrices                                             costs.Prices
	Now                                                    func() time.Time
}

type Handler struct {
	config Config
	schema *jsonschema.Schema
	mux    *http.ServeMux
	mu     sync.Mutex
}

type serviceRequest struct {
	Metadata struct{ Name, Owner, Requester, RequestID, Description string } `json:"metadata"`
	Spec     struct {
		Template struct{ ID, Version string } `json:"template"`
	} `json:"spec"`
}

type auditEvent struct {
	ID         string            `json:"id"`
	OccurredAt string            `json:"occurredAt"`
	Actor      map[string]string `json:"actor"`
	Action     string            `json:"action"`
	Subject    map[string]string `json:"subject"`
	RequestID  string            `json:"requestId"`
	Outcome    string            `json:"outcome"`
}

type submission struct {
	RequestID       string          `json:"requestId"`
	ServiceName     string          `json:"serviceName"`
	RequestState    string          `json:"requestState"`
	RepositoryState string          `json:"repositoryState"`
	RequestHash     string          `json:"requestHash"`
	Actor           string          `json:"actor"`
	Request         json.RawMessage `json:"request"`
	AuditEvents     []auditEvent    `json:"auditEvents"`
	CreatedAt       string          `json:"createdAt"`
	FailureCode     string          `json:"failureCode,omitempty"`
	CostEstimate    costs.Estimate  `json:"costEstimate"`
}

type submissionResponse struct {
	RequestID       string         `json:"requestId"`
	ServiceName     string         `json:"serviceName"`
	CostEstimate    costs.Estimate `json:"costEstimate"`
	RequestState    string         `json:"requestState"`
	RepositoryState string         `json:"repositoryState"`
}

type renderMarker struct {
	RequestID   string `json:"requestId"`
	RequestHash string `json:"requestHash"`
}

func New(config Config) (*Handler, error) {
	if config.Authenticate == nil || config.Authorize == nil {
		return nil, errors.New("authenticate and authorize functions are required")
	}
	if config.CostPrices.Version == "" {
		config.CostPrices = costs.FallbackPrices()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	for name, value := range map[string]string{
		"schema directory": config.SchemaDirectory, "template directory": config.TemplateDirectory, "generated directory": config.GeneratedDirectory,
		"GitHub organization": config.GitHubOrganization, "AWS region": config.AWSRegion, "GitHub Actions role ARN": config.GitHubActionsRoleARN,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	schema, err := compileServiceRequestSchema(config.SchemaDirectory)
	if err != nil {
		return nil, err
	}
	handler := &Handler{config: config, schema: schema, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	handler.mux.HandleFunc("POST /v1/service-requests", handler.createServiceRequest)
	return handler, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.mux.ServeHTTP(response, request)
}

func (handler *Handler) createServiceRequest(response http.ResponseWriter, request *http.Request) {
	actor, err := handler.config.Authenticate(request)
	if err != nil || actor == "" {
		writeError(response, http.StatusUnauthorized, "authentication_required", "Authentication is required.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, 1<<20))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "The request body is invalid or too large.")
		return
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "The request body must be valid JSON.")
		return
	}
	if err := handler.schema.Validate(document); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_service_request", "The service request does not match the platform contract.")
		return
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "The request could not be processed.")
		return
	}
	var desired serviceRequest
	if err := json.Unmarshal(canonical, &desired); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_service_request", "The service request does not match the platform contract.")
		return
	}
	if desired.Metadata.Requester != actor {
		writeError(response, http.StatusForbidden, "requester_mismatch", "The authenticated actor must match metadata.requester.")
		return
	}
	if err := handler.config.Authorize(actor, desired.Metadata.Owner); err != nil {
		writeError(response, http.StatusForbidden, "owner_forbidden", "The actor is not authorized to request a service for this owner.")
		return
	}
	if desired.Spec.Template.ID != "go-chi-api" || desired.Spec.Template.Version != "v0.1.0" {
		writeError(response, http.StatusBadRequest, "unsupported_template", "The requested template version is not installed.")
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.submit(response, desired, canonical, actor)
}

func (handler *Handler) submit(response http.ResponseWriter, desired serviceRequest, canonical []byte, actor string) {
	hashBytes := sha256.Sum256(canonical)
	requestHash := hex.EncodeToString(hashBytes[:])
	recordPath := filepath.Join(handler.config.GeneratedDirectory, "requests", desired.Metadata.RequestID+".json")
	if existing, err := readSubmission(recordPath); err == nil {
		if existing.RequestHash != requestHash {
			writeError(response, http.StatusConflict, "request_id_conflict", "The requestId is already bound to different content.")
			return
		}
		if existing.RepositoryState != "repository_creating" {
			if err := handler.reconcileRender(recordPath, &existing); err != nil {
				writeError(response, http.StatusConflict, "repository_render_failed", "The repository render could not be recovered.")
				return
			}
		}
		writeJSON(response, http.StatusOK, responseFor(existing))
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(response, http.StatusInternalServerError, "persistence_failed", "Stored request metadata could not be read.")
		return
	}

	now := handler.config.Now().UTC()
	estimate, err := costs.Calculate(costs.Input{Region: handler.config.AWSRegion, Request: canonical, CalculatedAt: now, Prices: handler.config.CostPrices})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cost_estimate_failed", "The pre-provision cost estimate could not be calculated.")
		return
	}
	eventID, err := randomUUID()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "The request could not be processed.")
		return
	}
	record := submission{
		RequestID: desired.Metadata.RequestID, ServiceName: desired.Metadata.Name, RequestState: "draft", RepositoryState: "repository_pending",
		RequestHash: requestHash, Actor: actor, Request: append(json.RawMessage(nil), canonical...), CreatedAt: now.Format(time.RFC3339Nano),
		AuditEvents:  []auditEvent{{ID: eventID, OccurredAt: now.Format(time.RFC3339Nano), Actor: map[string]string{"type": "user", "id": actor}, Action: "request.created", Subject: map[string]string{"type": "request", "id": desired.Metadata.RequestID}, RequestID: desired.Metadata.RequestID, Outcome: "succeeded"}},
		CostEstimate: estimate,
	}
	if err := writeSubmission(recordPath, record); err != nil {
		writeError(response, http.StatusInternalServerError, "persistence_failed", "The service request could not be persisted.")
		return
	}
	if err := handler.reconcileRender(recordPath, &record); err != nil {
		writeError(response, http.StatusConflict, "repository_render_failed", "The repository could not be rendered for this service name.")
		return
	}
	writeJSON(response, http.StatusCreated, responseFor(record))
}

func (handler *Handler) reconcileRender(recordPath string, record *submission) (resultErr error) {
	record.RepositoryState = "repository_rendering"
	record.FailureCode = ""
	if err := writeSubmission(recordPath, *record); err != nil {
		return err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		record.RepositoryState = "repository_failed"
		record.FailureCode = "repository_render_failed"
		eventID, err := randomUUID()
		if err == nil {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			record.AuditEvents = append(record.AuditEvents, auditEvent{
				ID: eventID, OccurredAt: now, Actor: map[string]string{"type": "system", "id": "platform-api"},
				Action: "repository.failed", Subject: map[string]string{"type": "repository", "id": record.ServiceName},
				RequestID: record.RequestID, Outcome: "failed",
			})
		}
		if persistErr := writeSubmission(recordPath, *record); persistErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("persist repository failure: %w", persistErr))
		}
	}()
	finalPath := filepath.Join(handler.config.GeneratedDirectory, "repositories", record.ServiceName)
	if markerMatches(finalPath, record.RequestID, record.RequestHash) {
		record.RepositoryState = "repository_creating"
		return writeSubmission(recordPath, *record)
	}
	if _, err := os.Stat(finalPath); err == nil {
		return errors.New("service repository is owned by another request")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stagingPath := filepath.Join(handler.config.GeneratedDirectory, "rendering", record.RequestID)
	if err := os.RemoveAll(stagingPath); err != nil {
		return err
	}
	var desired serviceRequest
	if err := json.Unmarshal(record.Request, &desired); err != nil {
		return err
	}
	description := strings.TrimSpace(desired.Metadata.Description)
	if description == "" {
		description = "Service " + desired.Metadata.Name
	}
	input := goldenpath.GoChiInputs{
		ServiceName: desired.Metadata.Name, Description: description, Owner: desired.Metadata.Owner, GitHubOrganization: handler.config.GitHubOrganization,
		ModulePath: "github.com/" + handler.config.GitHubOrganization + "/" + desired.Metadata.Name, AWSRegion: handler.config.AWSRegion,
		ECRRepository: "platform/" + desired.Metadata.Name, GitHubActionsRoleARN: handler.config.GitHubActionsRoleARN,
	}
	if err := goldenpath.RenderGoChi(handler.config.TemplateDirectory, stagingPath, input); err != nil {
		return err
	}
	markerData, err := json.Marshal(renderMarker{record.RequestID, record.RequestHash})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stagingPath, ".platform", "render.json"), append(markerData, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
		return err
	}
	if err := os.Rename(stagingPath, finalPath); err != nil && !markerMatches(finalPath, record.RequestID, record.RequestHash) {
		return err
	}
	record.RepositoryState = "repository_creating"
	record.FailureCode = ""
	return writeSubmission(recordPath, *record)
}

func markerMatches(repositoryPath, requestID, requestHash string) bool {
	data, err := os.ReadFile(filepath.Join(repositoryPath, ".platform", "render.json"))
	if err != nil {
		return false
	}
	var marker renderMarker
	return json.Unmarshal(data, &marker) == nil && marker.RequestID == requestID && marker.RequestHash == requestHash
}

func responseFor(record submission) submissionResponse {
	return submissionResponse{
		RequestID: record.RequestID, ServiceName: record.ServiceName, CostEstimate: record.CostEstimate,
		RequestState: record.RequestState, RepositoryState: record.RepositoryState,
	}
}

func compileServiceRequestSchema(directory string) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	for _, name := range []string{"common.schema.json", "helm-values.schema.json"} {
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		document, decodeErr := jsonschema.UnmarshalJSON(file)
		_ = file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode %s: %w", name, decodeErr)
		}
		if err := compiler.AddResource("https://platform-idp.dev/schemas/"+name, document); err != nil {
			return nil, err
		}
	}
	schema, err := compiler.Compile(filepath.Join(directory, "service-request.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile service request schema: %w", err)
	}
	return schema, nil
}

func readSubmission(path string) (submission, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return submission{}, err
	}
	var value submission
	if err := json.Unmarshal(data, &value); err != nil {
		return submission{}, err
	}
	return value, nil
}

func writeSubmission(path string, value submission) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".request-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}
func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
