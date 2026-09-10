# token: Atlas trust state and token verification

[internal SPEC](../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

Atlas signs the tokens that let one Metal host call another. Package `token` holds the Atlas trust state of one host and verifies the tokens that arrive with it.

The package owns the trust state and nothing else. It reads no virtual machine, image, or host state, and it makes no decision about a route.

## Types

| Type | Responsibility |
|---|---|
| `TrustedKeys` | The complete trust state: issuer, receiver, and public keys. |
| `PublicKey` | One Atlas signing key: an identifier and a raw Ed25519 public key in base64url without padding. |
| `KeyStore` | Owns the trusted key file and the copy that readers use. |
| `Claims` | The validated content of one token: VM identifier, caller, and scopes. |
| `Scope` | One permission: `read_vm` or `migration`. |

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
   ├─ the update is not valid ---------> ErrInvalidKeys, nothing changes
   ├─ the issuer or receiver changed --> ErrKeyConflict, nothing changes
   ├─ the file write fails ------------> an error, nothing changes
   └─ the file is published -----------> the memory copy is swapped
```

`Keys` returns a copy of the trust state, so a reader cannot change what the store holds.

## Validation

Trust state needs an issuer, a receiver, and at least one public key. Each key needs an identifier that no other key in the set uses, and a value that decodes to a 32-byte Ed25519 public key. `Validate` returns `ErrInvalidKeys` for anything else.

The issuer and the receiver are fixed after the first successful update. A later update that changes either one returns `ErrKeyConflict`. An update that repeats the current state is safe.

Two trusted keys let Atlas rotate a key without a gap. Metal accepts a token from any key in the set.

## Verification

`Verify` takes the trust state, one signed token, and the scopes the caller must hold. Ed25519 is the only accepted signature. The token header names the key with `kid`.

```text
Verify(keys, token, required scopes)
   |
   ├─ the host trusts no key ------------> ErrUnauthorized
   ├─ alg is not EdDSA ------------------> ErrUnauthorized
   ├─ kid names no trusted key ----------> ErrUnauthorized
   ├─ the signature does not match ------> ErrUnauthorized
   ├─ iss, aud, or exp is wrong ---------> ErrUnauthorized
   ├─ vm_id, sub, or scope is unusable --> ErrUnauthorized
   ├─ a required scope is absent --------> ErrForbidden
   └─ accepted --------------------------> Claims
```

| Claim | Rule |
|---|---|
| `iss` | Equals the stored issuer. |
| `sub` | The caller. Any value that is not empty. |
| `aud` | Contains the stored receiver. |
| `exp` | Required, and in the future. |
| `vm_id` | Required. The caller matches it to its own request. |
| `scope` | `read_vm`, `migration`, or both. An unknown scope makes the complete token unusable. |

`Verify` returns the caller and the VM identifier, and it compares neither to a request. The handler does that comparison, because only the handler knows which VM the request addresses.

Atlas limits how long a token stays usable. Metal reads `exp` and applies no maximum of its own.

## Related

- [internal/platform/SPEC.md](../platform/SPEC.md) publishes the file with an atomic rename.
