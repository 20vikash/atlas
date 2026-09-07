// Copyright (c) 2026, Frappe and contributors
// For license information, please see license.txt

frappe.ui.form.on("Proxy Server", {
	refresh(frm) {
		if (frm.is_new()) {
			return;
		}

		[
			[__("Provision"), "provision", frm.doc.status !== "Archived", "Actions"],
			[__("Push configuration"), "push_configuration", frm.doc.virtual_machine, "Actions"],
			[__("Install package"), "install_package", frm.doc.virtual_machine, "Actions"],
			[
				__("Update DNS record"),
				"update_dns_record",
				frm.doc.status !== "Archived" && frm.doc.server_ip_address,
				"Actions",
			],
			[__("Archive"), "archive", frm.doc.status !== "Archived", "Dangerous Actions"],
		].forEach(([label, method, condition, group]) => {
			if (!condition) {
				return;
			}

			frm.add_custom_button(
				label,
				() => {
					frappe.confirm(`Are you sure you want to ${label.toLowerCase()}?`, () =>
						frm
							.call(method, {
								freeze: true,
								freeze_message: __("Please wait..."),
							})
							.then(() => frm.refresh())
					);
				},
				__(group)
			);
		});
	},
});
