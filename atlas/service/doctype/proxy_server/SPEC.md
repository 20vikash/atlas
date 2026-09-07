# Proxy Server specification

[Service module specification](../../SPEC.md)

## Purpose

Proxy Server owns one regional HTTP proxy and its virtual machine. It stores only the virtual machine link. The creation dialog collects the VM image and size, then creates the VM before it starts proxy setup.

## Lifecycle

```text
create the VM -> update DNS -> wait for SSH -> install the package -> push the configuration
```

Atlas creates the Proxy Server record before it sends the VM request to Metal, then saves the VM name. Metal can return a draft VM. The reconciler settles it before another Re-provision action continues.

Atlas queues a setup job for each Pending Proxy Server every minute. A proxy remains Pending while its VM is a draft, so setup resumes after VM reconciliation. A failed setup changes the status to Failed and needs a Re-provision action. Re-provision queues every setup step and skips an unchanged package or configuration.

Only System Managers with System User accounts can use Proxy Server actions or create a Proxy Server.

Atlas points `<proxy-name>.<wildcard-domain>` at the reserved address. DNS updates and Archive lock the Proxy Server record. Archive removes the DNS record before it releases the address.

## Configuration

Atlas reserves the public IPv4 address and adds its public key when it creates the VM. Atlas uses `SSHRunner` to write the configuration because it contains a private key and a control credential. An `SSH Task` stores its script as plain text.

## Package

Atlas creates an archive from `services/http-proxy/` and publishes it as a public File. The source digest skips an unchanged build. Atlas keeps a replaced File until no Atlas Settings link refers to it.

Use the [HTTP proxy specification](../../../../services/http-proxy/SPEC.md) for the packaged component. Use [HTTP proxy setup](../../../../services/http-proxy/docs/setup.md) for proxy VM configuration. Use [Wildcard TLS](../../../docs/wildcard-tls.md) for certificate renewal.
