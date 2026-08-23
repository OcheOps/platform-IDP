# Authorization and Approval Policy

GitHub organization membership is the v1 identity source. Access is denied when
membership cannot be verified. Team-to-role mappings are configured by a
platform owner and changes to those mappings are audited.

| Capability | Developer | Engineering owner | Platform owner | Viewer |
|---|:---:|:---:|:---:|:---:|
| View catalogue and non-secret status | Yes | Yes | Yes | Yes |
| Request a service owned by their team | Yes | Yes | Yes | No |
| Change approved service fields | Yes | Yes | Yes | No |
| Approve as engineering owner | No | Yes | No | No |
| Approve as platform owner | No | No | Yes | No |
| Change shared chart or policy | No | No | Yes | No |
| Map GitHub teams to roles | No | No | Yes | No |
| Create/rotate a secret reference | Owned service | Owned service | Yes | No |
| Read an existing secret value | No | No | No | No |
| Request protected teardown | Owned service | Owned service | Yes | No |

An individual can hold several roles but cannot provide both approvals for one
request. The requester cannot approve that request. Authorization is checked at
request time and again immediately before merge/apply; cached authorization is
not sufficient for a privileged mutation.

Preview creation is authorized by the application repository's approved pull
request workflow and bounded by team quotas. It does not require staging
approvals because previews cannot target staging resources or persist past TTL.
