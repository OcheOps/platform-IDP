package databasejob_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/databasejob"
	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

const requestID = "f4d26f61-ef7d-4bd9-9568-ae621d91187f"

var image = "123456789012.dkr.ecr.eu-west-1.amazonaws.com/platform/database-provisioner@sha256:" + strings.Repeat("a", 64)

type fakeStore struct {
	works          []databaseworker.Work
	started, ready []string
	failed         map[string]string
}

func (store *fakeStore) PendingDatabases(context.Context) ([]databaseworker.Work, error) {
	return store.works, nil
}
func (store *fakeStore) Started(_ context.Context, id string) error {
	store.started = append(store.started, id)
	return nil
}
func (store *fakeStore) Ready(_ context.Context, id string) error {
	store.ready = append(store.ready, id)
	return nil
}
func (store *fakeStore) Failed(_ context.Context, id, code string) error {
	if store.failed == nil {
		store.failed = map[string]string{}
	}
	store.failed[id] = code
	return nil
}

type fakeCluster struct {
	objects map[string]map[string]any
	applied []string
}

func (cluster *fakeCluster) Apply(_ context.Context, path string, object any) error {
	if _, exists := cluster.objects[path]; exists {
		return errors.New("unexpected re-apply of an existing object")
	}
	data, _ := json.Marshal(object)
	var decoded map[string]any
	_ = json.Unmarshal(data, &decoded)
	cluster.objects[path] = decoded
	cluster.applied = append(cluster.applied, path)
	return nil
}

func (cluster *fakeCluster) Get(_ context.Context, path string, out any) (bool, error) {
	object, ok := cluster.objects[path]
	if !ok {
		return false, nil
	}
	data, _ := json.Marshal(object)
	return true, json.Unmarshal(data, out)
}

func (cluster *fakeCluster) setCondition(path, kind, reason string) {
	cluster.objects[path]["status"] = map[string]any{"conditions": []any{map[string]any{"type": kind, "status": "True", "reason": reason}}}
}

func work() databaseworker.Work {
	return databaseworker.Work{
		ServiceName: "orders-api", RequestID: requestID, DatabaseName: "svc_orders_api_8c7a25e2", DatabaseUser: "usr_orders_api_8c7a25e2",
		DatabaseHost: "shared.internal", DatabasePort: 5432, MasterSecretName: "platform/small-team-idp/shared/postgres-master",
		PasswordSecretName: "platform/small-team-idp/orders-api/staging/internal/database-password", SecretName: "platform/small-team-idp/orders-api/staging/database",
	}
}

func newWorker(t *testing.T, store *fakeStore, cluster *fakeCluster) *databasejob.Worker {
	t.Helper()
	worker, err := databasejob.New(store, cluster, databasejob.Config{Namespace: "platform-system", ServiceAccount: "database-provisioner", Image: image, AWSRegion: "eu-west-1", VPCCIDR: "10.42.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestWorkerLaunchesJobThenMarksReadyOnCompletion(t *testing.T) {
	store := &fakeStore{works: []databaseworker.Work{work()}}
	cluster := &fakeCluster{objects: map[string]map[string]any{}}
	worker := newWorker(t, store, cluster)
	name := databasejob.Name("orders-api", requestID)
	jobPath := "/apis/batch/v1/namespaces/platform-system/jobs/" + name
	policyPath := "/apis/networking.k8s.io/v1/namespaces/platform-system/networkpolicies/" + name

	if err := worker.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cluster.applied) != 2 || cluster.applied[0] != policyPath || cluster.applied[1] != jobPath {
		t.Fatalf("network policy must be applied before the Job: %v", cluster.applied)
	}
	if len(store.started) != 1 || len(store.ready) != 0 {
		t.Fatalf("started=%v ready=%v", store.started, store.ready)
	}
	encoded, _ := json.Marshal(cluster.objects[jobPath])
	for _, want := range []string{image, `"serviceAccountName":"database-provisioner"`, `"automountServiceAccountToken":false`, `"readOnlyRootFilesystem":true`, `"AWS_EC2_METADATA_DISABLED"`, `"value":"5432"`, `"platform.idp/request-id":"` + requestID + `"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("Job manifest is missing %s: %s", want, encoded)
		}
	}
	if strings.Contains(strings.ToLower(string(encoded)), "password\":") {
		t.Errorf("Job manifest must carry secret names only: %s", encoded)
	}

	if err := worker.Reconcile(context.Background()); !errors.Is(err, databasejob.ErrNoWork) {
		t.Fatalf("running Job should report no progress, got %v", err)
	}
	cluster.setCondition(jobPath, "Complete", "")
	if err := worker.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.ready) != 1 || store.ready[0] != requestID || len(cluster.applied) != 2 {
		t.Fatalf("ready=%v applied=%v", store.ready, cluster.applied)
	}
}

func TestWorkerRecordsJobFailures(t *testing.T) {
	for reason, code := range map[string]string{"BackoffLimitExceeded": "database_job_failed", "DeadlineExceeded": "database_job_deadline_exceeded"} {
		store := &fakeStore{works: []databaseworker.Work{work()}}
		cluster := &fakeCluster{objects: map[string]map[string]any{}}
		worker := newWorker(t, store, cluster)
		if err := worker.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		cluster.setCondition("/apis/batch/v1/namespaces/platform-system/jobs/"+databasejob.Name("orders-api", requestID), "Failed", reason)
		if err := worker.Reconcile(context.Background()); err == nil {
			t.Fatal("expected Job failure to be reported")
		}
		if store.failed[requestID] != code || len(store.ready) != 0 {
			t.Fatalf("%s: failed=%v ready=%v", reason, store.failed, store.ready)
		}
	}
}

func TestWorkerRejectsJobOwnedByAnotherRequest(t *testing.T) {
	store := &fakeStore{works: []databaseworker.Work{work()}}
	path := "/apis/batch/v1/namespaces/platform-system/jobs/" + databasejob.Name("orders-api", requestID)
	cluster := &fakeCluster{objects: map[string]map[string]any{path: {
		"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "database-provisioner", "platform.idp/request-id": "other"}},
		"status":   map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}},
	}}}
	if err := newWorker(t, store, cluster).Reconcile(context.Background()); err == nil {
		t.Fatal("expected conflict")
	}
	if store.failed[requestID] != "database_job_conflict" || len(store.ready) != 0 {
		t.Fatalf("failed=%v ready=%v", store.failed, store.ready)
	}
}

func TestWorkerContinuesPastInvalidRequests(t *testing.T) {
	invalid := work()
	invalid.RequestID = "a1b2c3d4-0000-4000-8000-000000000000"
	invalid.ServiceName = "Bad_Name"
	store := &fakeStore{works: []databaseworker.Work{invalid, work()}}
	cluster := &fakeCluster{objects: map[string]map[string]any{}}
	if err := newWorker(t, store, cluster).Reconcile(context.Background()); err == nil {
		t.Fatal("expected invalid request to be reported")
	}
	if store.failed[invalid.RequestID] != "database_job_invalid" || len(cluster.applied) != 2 {
		t.Fatalf("failed=%v applied=%v", store.failed, cluster.applied)
	}
}

func TestConfigRequiresDigestPinnedImage(t *testing.T) {
	config := databasejob.Config{Namespace: "platform-system", ServiceAccount: "database-provisioner", Image: "registry.example/provisioner:latest", AWSRegion: "eu-west-1", VPCCIDR: "10.42.0.0/16"}
	if err := config.Validate(); err == nil {
		t.Fatal("accepted mutable image tag")
	}
	config.Image = image
	config.VPCCIDR = "not-a-cidr"
	if err := config.Validate(); err == nil {
		t.Fatal("accepted invalid VPC CIDR")
	}
}

func TestNameMatchesChartHelper(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	output, err := exec.Command("helm", "template", "orders-db", "../../charts/database-provisioner", "--set", "image.repository=example.com/provisioner",
		"--set", "image.digest="+digest, "--set", "request.id="+requestID, "--set", "request.service=orders-api",
		"--set", "awsRegion=eu-west-1", "--set", "database.name=svc_orders_api_8c7a25e2", "--set", "database.user=usr_orders_api_8c7a25e2",
		"--set", "database.host=shared.internal", "--set", "secrets.master=master", "--set", "secrets.password=password", "--set", "secrets.service=service").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v: %s", err, output)
	}
	name := databasejob.Name("orders-api", requestID)
	if strings.Count(string(output), "name: "+name+"\n") != 2 {
		t.Fatalf("chart Job/NetworkPolicy names do not match %s:\n%s", name, output)
	}
	long := databasejob.Name(strings.Repeat("a", 60), requestID)
	if len(long) > 63 || strings.HasSuffix(long, "-") {
		t.Fatalf("long name = %s", long)
	}
}
