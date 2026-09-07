// Copyright (c) 2026, Frappe and contributors
// For license information, please see license.txt

frappe.ui.form.on("Proxy Server", {
	refresh(frm) {
		frm.disable_save();
		if (frm.is_new()) {
			return;
		}

		[
			[__("Re-provision"), "provision", frm.doc.status !== "Archived", "Actions"],
			[
				__("Update DNS record"),
				"update_dns_record",
				frm.doc.status !== "Archived" && frm.doc.virtual_machine,
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

		frm.call("get_domain").then(({ message: domain }) => {
			if (domain) {
				frm.add_web_link(`https://${domain}`, __("Open proxy server"));
			}
		});

		frm.add_custom_button(
			__("Show control API password"),
			() => {
				frm.call("get_control_api_password").then(({ message: password }) => {
					frappe.msgprint({
						title: __("Control API password"),
						message: `<code>${frappe.utils.escape_html(password)}</code>`,
					});
				});
			},
			__("Actions")
		);
	},
});
