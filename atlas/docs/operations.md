# Atlas operations

Use Frappe Desk Error Log and the named documents first. Keep the durable Atlas record until the external owner confirms the result.

## Proxy cluster cannot accept writes

- Symptom: The proxy API returns `503`, or `/readyz` returns `503` on several nodes. The nodes still answer `/healthz` and route traffic.
- Safe checks: Call `GET /v1/cluster/status` on node addresses. Compare `leader_id`, `term`, `generation`, `members`, and `ready`. Check `atlas-proxy-control.service` logs.
- Expected evidence: A ready majority has one leader and matching generations. A five-node cluster needs three write acknowledgements.
- Safe recovery: Restore enough configured peers for the required acknowledgement count. Retry the same desired map mutation after connectivity returns.
- Do not: Do not edit `cluster-state.json` or reduce a generation by hand.

## Proxy node stays outside regional DNS

- Symptom: A Proxy Server is Failed, or its address is absent from `proxy.<wildcard-domain>`.
- Safe checks: Read the failed provisioning phase. Check the node A record, `/readyz`, its `/healthz` health check, and configuration push tasks on active peers.
- Expected evidence: Atlas publishes regional DNS only after the node is ready. Route 53 normally omits an unhealthy node and returns all records if every health check fails.
- Safe recovery: Correct the failed phase and select Re-provision. Atlas repeats safe steps and reconciles active node configurations every minute.
- Do not: Do not add an unready node to regional DNS by hand.

## Atlas cannot reach Metal

- Symptom: A virtual machine read fails, or Metal Server synchronization writes a Metal connection Error Log.
- Owner: Network access, the Metal Server, or Metal.
- Safe checks: Open the Metal Server document. Check its public IPv4 address and Metal API token. Check recent Error Logs. On the host, run `systemctl status metald` and `journalctl -u metald --since "15 minutes ago"`.
- Expected evidence: The log names the Metal Server and keeps the connection error. Metal logs show the same request time or no received request.
- Safe recovery: Restore network access or restart `metald`. Wait for the next synchronization. Repeat an idempotent user action only after you read current state.
- Do not: Do not delete the Atlas virtual machine record. Do not copy a token into logs or chat.

## Desired and observed generations do not match

- Symptom: `desired.generation` stays greater than `observed.generation`.
- Owner: Metal reconciliation or one host resource owner.
- Safe checks: Call `GET /v1/vms/{id}` with an approved local token source. Check `observed.error` and the Metal operation logs for the virtual machine ID.
- Expected evidence: The observed state shows the last applied generation and a safe error when reconciliation stopped.
- Safe recovery: Correct the named host fault. Let reconciliation retry. Send the same desired request only when you must wake reconciliation.
- Do not: Do not edit Metal record files. Do not increase a generation by hand.

## Virtual machine stays unknown or failed

- Symptom: The Atlas form shows `unknown` or `failed` after the normal reconcile interval.
- Owner: An uncertain Atlas create request or Metal runtime reconciliation.
- Safe checks: Check whether the Atlas record is a draft. Read `GET /v1/vms/{id}`. Check the Metal log and `systemctl status metal-vm@<id>.service`.
- Expected evidence: HTTP `404` confirms an absent draft. A present record reports desired state, observed state, and an error.
- Safe recovery: Let draft reconciliation finalize or remove a confirmed absent draft. Correct a reported host error and let Metal retry.
- Do not: Do not delete an uncertain draft before Metal returns HTTP `404`. Do not start Firecracker outside systemd.

## Metal Server stays Pending, Installing, or Failed

- Symptom: Metal Server setup does not reach Running.
- Owner: Provider setup, Secure Shell access, host installation, or Metal configuration.
- Safe checks: Open the Metal Server and its setup task. Read the latest Error Log. Check the recorded failed phase. Test provider state and root Secure Shell access.
- Expected evidence: The Metal Server keeps completed fields. The log names the Metal Server and failed phase.
- Safe recovery: Correct the phase fault. Use Setup Metal Server again. Each completed operation is safe to repeat.
- Do not: Do not clear `provider_server_id`. Do not delete a reused provider host.

## Capacity samples become old

- Symptom: The newest Metal Server Usage record is more than 2 minutes old.
- Owner: Atlas Metal Server synchronization or Metal capacity inspection.
- Safe checks: Check recent Metal Server synchronization Error Logs. Check that the Metal Server is Running and provisioning is complete. Check `metald` logs.
- Expected evidence: The Error Log distinguishes a connection failure from an invalid capacity response.
- Safe recovery: Restore Metal access or correct the host inspection fault. Wait for a new Metal Server Usage record.
- Do not: Do not change the creation time of an old sample. Do not estimate free capacity by hand.

## Placement reports no capacity

- Symptom: Create Virtual Machine reports no current sample or no Metal Server with enough capacity.
- Owner: Atlas placement when samples are missing, or Metal host capacity when samples are current.
- Safe checks: Compare the image architecture, newest Metal Server Usage values, and existing Atlas reservations. Check uncertain drafts.
- Expected evidence: Atlas identifies a missing sample separately. A current sample shows the limiting CPU, memory, or storage value.
- Safe recovery: Restore synchronization, remove only confirmed stale drafts, or add real host capacity.
- Do not: Do not remove a draft that Metal might have created. Do not edit capacity samples.

## Public IPv4 intent stays pending

- Symptom: A Metal Server IP Address stays Attaching or Detaching.
- Owner: The provider address operation or its reconcile job.
- Safe checks: Read the address, `intent_version`, Metal Server, and provider resource ID. Find the Error Log that names the same address, action, and version. Check provider state.
- Expected evidence: A failed job leaves the current intent unchanged. An old job cannot complete a newer version.
- Safe recovery: Correct provider access and wait for the scheduled retry. Queue reconcile again only for the current intent.
- Do not: Do not reduce `intent_version`. Do not attach or detach the provider address manually while Atlas has pending intent.

## Machine image transfer does not finish

- Symptom: A Machine image stays Pending, Uploading, Completing, Cleaning, or Failed.
- Owner: Metal snapshot staging, the object storage multipart upload, or Atlas finalization.
- Safe checks: Open the image and record its status, error, source Metal Server, snapshot ID, object keys, and upload IDs. Check Atlas and Metal logs without printing signed URLs.
- Expected evidence: Failed transfers keep all retry identifiers. Completed uploads have both SHA-256 values before cleanup.
- Safe recovery: Correct Metal or object storage access. Use Retry Transfer for a Failed image. Let periodic work advance an active image.
- Do not: Do not clear upload IDs before completion. Do not delete Metal staging while Atlas still needs it.
