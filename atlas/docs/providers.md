# Metal Server providers

Atlas uses `ServerProvider` as the server provider extension point. The registry maps one stable provider type to one provider class. Atlas has two providers: Scaleway and AWS.

## Ownership

Atlas owns provider selection, credentials, catalog records, and Metal Server documents. A provider owns remote resource operations.

Low-level provider components return values. They do not save Frappe documents or commit database transactions.

## Contract

The provider contract includes these operations:

- Validate settings and credentials.
- Set up named provider infrastructure.
- Return server sizes and images.
- Ensure one provider host by its discovery key.
- Prepare provider resources before Secure Shell access.
- Configure the provider network after Secure Shell access.
- Apply one explicit power action.
- Delete one provider host safely.
- Return the storage pool device.
- Optionally reserve, attach, detach, and delete public IPv4 addresses.

Optional address operations raise `UnsupportedProviderOperation` when the provider does not support them.

Creation uses `ServerCreateRequest` and returns `ProviderServer`. Catalog operations return `ServerSizeData` and `ServerImageData`.

## Add a provider

1. Add one package under `atlas/core/server_providers/`.
2. Implement `ServerProvider` with absolute imports.
3. Register the class with `register`.
4. Split remote operations by owned provider resource.
5. Keep Frappe document writes in Atlas owners.
6. Add tests for registration, retries, power failures, deletion, and optional operations.
7. Add the provider option and fields to Atlas Settings.
8. Update this guide and the related specification.

Use the persisted `ServerCreateRequest.discovery_key` for provider discovery. It is unique to one Metal Server record across Atlas sites. Keep the provider host ID in `provider_server_id`; the Metal Server name is only a label. The provider host is created only after Atlas commits the Pending Metal Server record. A retry uses the stored key to find the same host.

## Shared provider behavior

`ServerProvider` owns the operations that every provider repeats: `poll`, `run_setup_script`, `wait_for_private_address`, `apply_provider_server`, and `update_provider_metadata`. Set `error_class` on a provider class so these operations raise the error type of that provider.

## Scaleway structure

| Module | Owner |
|---|---|
| `configuration.py` | Immutable settings for low-level operations. |
| `client.py` | HTTP transport and Scaleway errors. |
| `infrastructure.py` | VPC, private network, and Secure Shell key operations. |
| `catalog.py` | Offer and operating system translation. |
| `servers.py` | Elastic Metal server operations. |
| `ip_addresses.py` | Flexible IP address operations. |
| `partitioning.py` | Provider disk layout. |
| `provider.py` | Contract composition and host network setup. |

## AWS structure

| Module | Owner |
|---|---|
| `configuration.py` | Immutable settings for low-level operations. |
| `client.py` | boto3 sessions, pagination, and AWS errors. |
| `infrastructure.py` | VPC, subnet, security group, key pair, and transit gateway operations. |
| `catalog.py` | Instance type and machine image translation. |
| `servers.py` | Instance, mesh interface, and multicast registration operations. |
| `ip_addresses.py` | Elastic IP address operations. |
| `provider.py` | Contract composition and host network setup. |

### Multicast discovery

WG Mesh finds a virtual machine with IPv4 multicast on `239.1.1.1`. An AWS VPC subnet does not carry multicast, so Atlas creates one transit gateway multicast domain for the region.

Atlas registers each host statically as a multicast group member and a multicast group source. It does not use IGMPv2. WG Mesh reads discovery frames with an eBPF hook on the uplink and never joins the group with a socket, so the host sends no IGMP membership report and dynamic membership would leave the domain empty.

WG Mesh sends discovery with a multicast time to live of 1. Atlas therefore uses one subnet in one availability zone for the whole region. Every host shares that subnet.

### Host network shape

An Atlas host gets a second network interface in the Atlas subnet. That interface carries mesh traffic and is the interface that Atlas registers with the multicast domain.

AWS gives no stable guest device name, so `aws/configure-private-network.sh` finds the interface by its MAC address and renames it to `atlas-mesh`. metald uses that name as its mesh uplink.

The script sends the IPv4 multicast range through `atlas-mesh`. It writes persistent configuration for Netplan or `systemd-networkd`.

Atlas stores the mesh network interface ID before it starts the attachment. AWS deletion uses only this stored ID. It verifies the interface attachment before it deletes the interface or the instance.

### Network exposure

The Atlas security group opens Secure Shell, the Metal API port, and the WireGuard port to the internet, because Atlas reaches a host through its public address. The Metal API uses a bearer token. Every other port is open only inside the Atlas private network.

### Instance types

Atlas needs hardware virtualization, local instance storage, and two network interfaces. The catalog rejects an instance type that does not provide them.

A bare metal type provides the processor extensions. A virtual type must report `nested-virtualization` in `ProcessorInfo.SupportedFeatures`.

AWS keeps nested virtualization off until an instance asks for it, so Atlas launches a virtual instance with `CpuOptions.NestedVirtualization` set to `enabled`. A bare metal instance rejects that option, so Atlas does not send it.

`DescribeInstanceTypes` reports no price, so the catalog prices stay empty.
