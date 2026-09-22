# Configure the HTTP proxy

Use this guide after [installing the proxy](setup.md). It covers the configuration file, configuration changes, node joins, and DNS.

## Configuration file

The following example configures `proxy-001` in a three-node cluster:

```toml
[control]
domain = "proxy.par-1.example.com"
node_domain = "proxy-001.par-1.example.com"
admin_socket = "/run/nginx/admin.sock"
cert_dir = "/var/lib/nginx/certs"

[auto_proxy]
address_prefix = "fdaa:1"
host_prefixes = ["site-", "*-vm-"]

[auth]
password_hash = "$2b$12$replace-with-the-current-bcrypt-hash"
previous_password_hash = "$2b$12$replace-with-the-previous-bcrypt-hash"
previous_password_valid_until = 1788800000
jwks_url = "https://atlas-42.example.com/api/atlas/jwks.json"
jwks_audience_id = "atlas-proxy:42"
jwks_issuers = ["central", "atlas:42"]

[cluster]
node_id = "proxy-001"
password = "replace-with-the-current-regional-password"
previous_password = "replace-with-the-previous-regional-password"
previous_password_valid_until = 1788800000
state_path = "/var/lib/nginx/cluster-state.json"
peers = [
  { node_id = "proxy-001", address = "https://proxy-001.par-1.example.com" },
  { node_id = "proxy-002", address = "https://proxy-002.par-1.example.com" },
  { node_id = "proxy-003", address = "https://proxy-003.par-1.example.com" },
]

[tls]
wildcard_domain = "*.par-1.example.com"
fullchain_pem = '''
-----BEGIN CERTIFICATE-----
...
-----END CERTIFICATE-----
'''
private_key_pem = '''
-----BEGIN PRIVATE KEY-----
...
-----END PRIVATE KEY-----
'''
```

`control.domain` is the regional address. `control.node_domain` is the local node address. Both names must be one label below the wildcard zone. The apply command writes both labels to `/var/lib/nginx/control-subdomain`, so OpenResty sends them to the daemon before it checks the site map.

`auto_proxy.address_prefix` contains the first two hextets of the regional VM mesh address. `auto_proxy.host_prefixes` lists literal prefixes. A leading `*` matches any non-empty prefix before the remaining text. Leave the section out to turn static routing off. Read [OpenResty data plane](openresty.md) for the label form.

`auth` protects the public map API. The daemon accepts the current password hash. It accepts the earlier hash until `previous_password_valid_until`. This value is a Unix time in seconds. The daemon gets Central and regional Atlas keys from `jwks_url`. The token audience must match `jwks_audience_id`. A token issuer must occur in `jwks_issuers` and match the key namespace.

`cluster` protects internal peer routes. It contains the raw regional passwords. It accepts the previous password until `previous_password_valid_until`. The peer array must include the local `node_id` and unique node IDs. It supports at most 5 entries. Every peer address must use HTTPS.

`tls` is required. The apply command refuses a missing certificate, a key that does not match, or a certificate that does not cover the wildcard name.

## Apply a changed configuration

Write the new file atomically, then run:

```sh
sudo /opt/atlas/proxy-control/bin/proxy-control
sudo systemctl restart atlas-proxy-control.service
```

The apply command installs the certificate, writes the region and control label files, and reloads OpenResty. Restart the daemon after a peer, password, or cluster setting changes.

## Join a node

1. Create the node A record.
2. Install the package and send a configuration that contains the current peer list.
3. Start the daemon.
4. Add the node to the configurations of active peers.
5. Wait for `/readyz` to return `204`.
6. Create its HTTPS health check and add its address to `proxy.<wildcard-domain>`.
7. Make sure that `*.<wildcard-domain>` is a CNAME to `proxy.<wildcard-domain>`.

At startup, the node reads its durable snapshot and asks peers for their status. It downloads the highest-generation peer snapshot when that snapshot is newer. The readiness route stays unavailable until the node has synchronized and knows a leader.

## DNS

Each node has one A record at `proxy-NNN.<wildcard-domain>` with a 3600-second TTL. The regional `proxy.<wildcard-domain>` name has one multivalue A record per node with a 120-second TTL.

Each regional record has an HTTPS health check for `/healthz`. The health check uses the node name as the TLS host name.

The `*.<wildcard-domain>` CNAME points to `proxy.<wildcard-domain>` and has a 3600-second TTL. Exact node and regional records take priority over this wildcard record.

Remove the regional record and health check before you release a node address. Then remove the node A record.
