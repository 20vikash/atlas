// Copyright (c) 2026, Frappe and contributors
// For license information, please see license.txt

function showProvisionDialog(frm) {
	const dialog = new frappe.ui.Dialog({
		title: __("Provision Cargo Server"),
		fields: [
			{
				fieldname: "virtual_machine_image",
				fieldtype: "Link",
				label: __("Virtual Machine Image"),
				options: "Virtual Machine Image",
				reqd: 1,
				filters: { enabled: 1, status: "Available", image_type: "system" },
			},
			{ fieldname: "vcpus", fieldtype: "Int", label: __("vCPUs"), reqd: 1, default: 2 },
			{
				fieldname: "memory_mib",
				fieldtype: "Int",
				label: __("Memory (MiB)"),
				reqd: 1,
				default: 4096,
			},
			{
				fieldname: "disk_mib",
				fieldtype: "Int",
				label: __("Disk (MiB)"),
				reqd: 1,
				default: 16384,
			},
			{
				fieldname: "server_ip_address",
				fieldtype: "Link",
				label: __("Public IPv4 Address"),
				options: "Metal Server IP Address",
				reqd: 1,
				filters: { status: "Allocated", tenant_id: 0, virtual_machine: ["is", "not set"] },
			},
		],
		primary_action_label: __("Provision"),
		primary_action(values) {
			frm.call({
				method: "provision",
				doc: frm.doc,
				args: { request: values },
				freeze: true,
				freeze_message: __("Creating Cargo Server..."),
			}).then(() => {
				dialog.hide();
				return frm.refresh();
			});
		},
	});
	dialog.show();
}

frappe.ui.form.on("Cargo Server", {
	refresh(frm) {
		frm.disable_save();
		if (!has_common(frappe.user_roles, ["System Manager"])) {
			return;
		}

		if (!frm.doc.virtual_machine && ["Not Provisioned", "Archived"].includes(frm.doc.status)) {
			frm.page.set_primary_action(__("Provision"), () => showProvisionDialog(frm));
		}

		if (frm.doc.virtual_machine) {
			frm.add_custom_button(
				__("Archive"),
				() =>
					frappe.confirm(__("Archive the Cargo Server?"), () =>
						frm
							.call({ method: "archive", doc: frm.doc, freeze: true })
							.then(() => frm.refresh())
					),
				__("Dangerous Actions")
			);
		}
	},
});
