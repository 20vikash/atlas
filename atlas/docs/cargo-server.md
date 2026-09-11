# Cargo Server

## Purpose

Cargo Server manages the single regional Cargo virtual machine. It installs Cargo and publishes `cargo.<wildcard-domain>` and `cargo-pilot.<wildcard-domain>` through the regional Proxy.

```text
Atlas SSH ----------------------> Cargo public IPv4
cargo.<wildcard-domain> -> Proxy -> Cargo mesh IPv6
cargo-pilot.<wildcard-domain> -> Proxy -> Cargo mesh IPv6
```

## Provision

Before you provision Cargo Server, make sure that at least one Proxy Server is Active. Reserve one tenant `0` public IPv4 address and make sure that the required System image is Available.

Open Cargo Server and select Provision. Select the image and public IPv4 address. The form defaults to 2 vCPUs, 4096 MiB of memory, and 16384 MiB of disk.

Atlas creates the virtual machine and sets Pending. A Pending service can stay in this status while Atlas reconciles an uncertain virtual machine create request. Do not start another provision request. The scheduled job continues with the same virtual machine.

Atlas changes the status to Provisioning when the virtual machine is ready. It waits for SSH on the public IPv4 address, installs Cargo with `cargo-pilot.<wildcard-domain>` as the Pilot administration domain, maps both public domains to the mesh IPv6 address, and checks the Cargo ping route. A successful check sets Active.

## Investigate a failure

Read Failure on Cargo Server. The value starts with the failed phase, such as `installation`, `proxy-routes`, or `readiness`.

Atlas does not automatically retry a Failed service and does not create a replacement virtual machine. Correct the cause and select Archive. Provision becomes available after Archive clears the virtual machine link and sets Archived.

## Archive

Select Archive to remove the Cargo and Pilot routes and terminate the virtual machine. Atlas stops if Proxy route removal fails. The virtual machine stays attached so that you can retry Archive after the Proxy is available.

After Atlas accepts the virtual machine termination request, Cargo Server immediately clears the virtual machine and installation task links and sets Archived. Metal completes virtual machine deletion independently. Virtual machine termination releases the address attachment but keeps the tenant `0` address reservation.

## Security

Atlas runs the installer in a synchronous SSH Task. The linked task stores the generated tokens, passwords, installer environment, and command output for System Manager visibility while the virtual machine exists. Virtual machine deletion also deletes its SSH Tasks.

The generated service tokens remain valid for 365 days. Atlas has no Cargo token revocation record. Treat a copied token as valid until it expires.

Use the [Cargo Server specification](../service/doctype/cargo_server/SPEC.md) for ownership and interface details.
