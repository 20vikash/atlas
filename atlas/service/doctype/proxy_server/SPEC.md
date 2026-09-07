# Proxy Server specification

[Service module specification](../../SPEC.md)

## Purpose

Proxy Server owns one regional HTTP proxy and its virtual machine.

## Lifecycle

```text
reserve an address -> create the VM -> update DNS -> wait for SSH -> install the package -> push the configuration
```

Atlas saves the VM name before it sends the create request to Metal. Metal can return a draft VM. The reconciler settles it before another Provision action continues.

Atlas points `<proxy-name>.<wildcard-domain>` at the reserved address. DNS updates and Archive lock the Proxy Server record. Archive removes the DNS record before it releases the address.

## Configuration

Atlas connects through the public IPv4 address and adds its public key when it creates the VM. Atlas uses `SSHRunner` to write the configuration because it contains a private key and a control credential. An `SSH Task` stores its script as plain text.

## Package

Atlas creates an archive from `services/http-proxy/` and publishes it as a public File. The source digest skips an unchanged build. Atlas keeps a replaced File until no Atlas Settings link refers to it.

Use the [HTTP proxy specification](../../../../services/http-proxy/SPEC.md) for the packaged component. Use [HTTP proxy setup](../../../../services/http-proxy/docs/setup.md) for proxy VM configuration. Use [Wildcard TLS](../../../docs/wildcard-tls.md) for certificate renewal.
