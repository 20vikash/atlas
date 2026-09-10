# token: Atlas trust state

[internal SPEC](../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

Atlas signs the tokens that let one Metal host call another. Package `token` holds the Atlas trust state of one host: the issuer, the receiver, and the Atlas public keys that the host trusts.

The package owns the trust state and nothing else. It reads no virtual machine, image, or host state.

## Types

| Type | Responsibility |
|---|---|
| `TrustedKeys` | The complete trust state: issuer, receiver, and public keys. |
| `PublicKey` | One Atlas signing key: an identifier and a raw Ed25519 public key in base64url without padding. |
| `KeyStore` | Owns the trusted key file and the copy that readers use. |

## Startup

The caller gives `NewKeyStore` the file path. The store reads the file once and keeps a copy in memory.

| Condition at startup | Result |
|---|---|
| The file is absent | The store is empty and Metal trusts no key. |
| The file is not valid JSON | `ErrInvalidKeys`. |
| The file holds trust state Metal cannot use | `ErrInvalidKeys`. |

## Update flow

`Replace` changes the file first and the memory copy last. A failed step leaves both unchanged, so a bad update cannot remove working keys.

```text
Replace(update)
   |
   ├─ validate ------------> invalid ------> ErrInvalidKeys, nothing changes
   ├─ compare identity ----> changed ------> ErrKeyConflict, nothing changes
   ├─ write the file ------> failed -------> error, nothing changes
   └─ swap the memory copy --------------->  Keys() returns the update
```

`Keys` returns a copy of the trust state, so a reader cannot change what the store holds.

## Validation

Trust state needs an issuer, a receiver, and at least one public key. Each key needs an identifier that no other key in the set uses, and a value that decodes to a 32-byte Ed25519 public key. `Validate` returns `ErrInvalidKeys` for anything else.

The issuer and the receiver are fixed after the first successful update. A later update that changes either one returns `ErrKeyConflict`. An update that repeats the current state is safe.

## Related

- [internal/platform/SPEC.md](../platform/SPEC.md) publishes the file with an atomic rename.
