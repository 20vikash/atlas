# Proxy Server

A Proxy Server is one regional HTTP proxy and the virtual machine Atlas runs it on. The record owns that machine: Atlas creates it, installs the published package, and pushes one configuration file.

## Setup

```text
create the record
  -> reserve a public IPv4 address
  -> create the virtual machine   uplink egress, the Atlas public key in guest.ssh_keys
  -> wait for root over SSH
  -> download the package and run nginx/setup.sh
  -> write /etc/atlas/proxy-control.toml and apply it
  -> Active
```

Every phase is safe to repeat, and each one saves its progress before the next starts. A failed run resumes at the phase that failed instead of building a second machine. Use the Provision action to run it again.

Atlas reaches the guest over its public IPv4 address, because the proxy needs one to serve site traffic. The Atlas public key goes into the machine at create time, so the controller can connect as soon as the guest boots. The base image reads its keys from MMDS on every login, so no key is stored in the image or in the Virtual Machine record.

Metal may not confirm a create. The record then keeps the machine name and the phase stops, because the virtual machine reconciler settles the draft first. Run Provision again after that.

## Configuration

The proxy takes its credentials and its wildcard certificate from one file. Atlas renders it and writes it over SSH at mode 0600, then runs `proxy-control` to install the certificate and reload OpenResty.

The push does not use an `SSH Task`. A task stores its script and environment as plain text, and this payload carries the wildcard private key and the control credential. Only the outcome is recorded.

`control_api_password` is generated once, when the record is created. Atlas keeps the password and the proxy keeps a bcrypt hash of it, so a proxy that an attacker reads gives up no credential.

A resend is skipped when nothing changed. The digest covers the inputs and not the rendered file, because a bcrypt hash is salted and would differ on every call.

## Certificate renewal

A change to the wildcard certificate, its private key, or the JWKS settings queues a configuration push to every Active Proxy Server. That covers both an automatic renewal and a certificate an operator pastes in. See [the wildcard TLS guide](wildcard-tls.md).

## Package updates

Atlas publishes the HTTP proxy component as one archive: [the Service specification](../service/SPEC.md). The Install Package action downloads the current archive, checks its SHA-256, and runs the setup script. It does nothing when the host already holds that package.

## Archive

The Archive action terminates the virtual machine and releases its address. Deletion is refused while the machine still exists, so the record cannot be lost before its resources are.
