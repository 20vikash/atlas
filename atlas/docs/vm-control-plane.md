# Virtual machine control plane

Atlas owns the user request, placement, and related provider intent. Metal owns desired host state, observed host state, and reconciliation.

## Create sequence

```text
request -> validate image and values -> select and lock Metal Server
        -> insert and commit draft -> PUT /v1/vms/{name}
        -> Metal saves desired state -> Metal reconciles host resources
        -> Atlas clears the draft after confirmation
```

The Atlas document name is the Metal virtual machine ID. The committed draft makes a lost create response safe. Atlas keeps an uncertain draft until `GET /v1/vms/{name}` confirms presence or returns HTTP `404`.

## Placement

Placement uses a capacity sample that is less than 2 minutes old. It matches the image architecture and subtracts requests that the sample cannot include. Every uncertain draft remains a reservation.

Atlas locks the candidate Metal Server row and checks capacity again. It commits the selected draft before it sends the Metal request.

## Desired and observed state

Atlas sends complete desired values for compute, disk, network, guest metadata, and Secure Shell keys. Metal stores these values before it returns HTTP `202`.

Metal reports desired and observed generations. Equal generations mean that Metal applied the current request. A different generation means that reconciliation still has work or stopped after an error.

Atlas does not copy mutable Metal state into durable DocType fields. Virtual fields read the current Metal response. A Metal read fault is visible to the user.

## Public IPv4 intent

Atlas owns public IPv4 assignment intent. Each change increases `intent_version`. A reconcile job applies one provider operation and completes it only if the stored version still matches.

This check prevents an old job from replacing a newer attach or detach request. A failed job keeps the pending intent and writes an Error Log with the address, action, and version.

## Transaction and retry boundaries

- Provider and Metal calls must be safe to repeat.
- Atlas commits a virtual machine draft before its external create request.
- Metal Server provisioning commits after each safe step.
- Background reconciliation keeps durable intent when an external operation fails.
- A retry reads current durable state. It does not depend on worker memory.

See [Atlas operations](operations.md) for fault checks and safe recovery.
