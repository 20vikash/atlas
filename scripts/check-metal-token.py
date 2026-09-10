"""Issue one Atlas-signed Metal token and write it beside the trust state."""

from __future__ import annotations

import json
import pathlib
import sys

import frappe

from atlas.metal_server.core.metal_token import (
	SCOPE_MIGRATION,
	SCOPE_READ_VIRTUAL_MACHINE,
	MetalTokenIssuer,
)

CALLER = "node-check-source"
RECEIVER = "node-check-target"
VIRTUAL_MACHINE = "vm-check-00001"


def main() -> None:
	site, directory = sys.argv[1], pathlib.Path(sys.argv[2])

	frappe.init(site=site)
	frappe.connect()
	try:
		if frappe.get_single("Atlas Settings").initialize_metal_token_key(persist=True):
			frappe.db.commit()  # nosemgrep

		issuer = MetalTokenIssuer(frappe.get_single("Atlas Settings"))
		key_sync = issuer.get_key_sync_payload(RECEIVER)
		token = issuer.issue_token(
			virtual_machine_id=VIRTUAL_MACHINE,
			caller=CALLER,
			receiver=RECEIVER,
			scopes=[SCOPE_READ_VIRTUAL_MACHINE, SCOPE_MIGRATION],
		)
	finally:
		frappe.destroy()

	(directory / "atlas-jwt.json").write_text(json.dumps(key_sync))
	(directory / "token").write_text(token)

	print(f"issuer      {key_sync['issuer']}")
	print(f"receiver    {key_sync['receiver']}")
	print(f"public keys {[key['id'][:16] + '...' for key in key_sync['public_keys']]}")


if __name__ == "__main__":
	main()
