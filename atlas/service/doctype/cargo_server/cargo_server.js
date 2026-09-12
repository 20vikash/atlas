// Copyright (c) 2026, Frappe and contributors
// For license information, please see license.txt

function storageClusterConfig(values) {
	return {
		storage_node_count: values.storage_node_count,
		replication_factor: values.replication_factor,
		gateway: {
			cpu: values.gateway_cpu,
			ram_gb: values.gateway_ram_gb,
			disk_gb: values.gateway_disk_gb,
		},
		storage: {
			cpu: values.storage_cpu,
			ram_gb: values.storage_ram_gb,
			disk_gb: values.storage_disk_gb,
		},
	};
}

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
			{ fieldtype: "Section Break", label: __("Storage Cluster") },
			{
				fieldname: "storage_node_count",
				fieldtype: "Int",
				label: __("Storage Nodes"),
				reqd: 1,
				default: 3,
			},
			{
				fieldname: "replication_factor",
				fieldtype: "Int",
				label: __("Replication Factor"),
				reqd: 1,
				default: 3,
				description: __(
					"Copies of each object. Storage nodes must not be fewer than this."
				),
			},
			{ fieldtype: "Column Break" },
			{
				fieldname: "gateway_cpu",
				fieldtype: "Int",
				label: __("Gateway vCPUs"),
				reqd: 1,
				default: 2,
			},
			{
				fieldname: "gateway_ram_gb",
				fieldtype: "Int",
				label: __("Gateway Memory (GB)"),
				reqd: 1,
				default: 4,
			},
			{
				fieldname: "gateway_disk_gb",
				fieldtype: "Int",
				label: __("Gateway Disk (GB)"),
				reqd: 1,
				default: 20,
			},
			{ fieldtype: "Column Break" },
			{
				fieldname: "storage_cpu",
				fieldtype: "Int",
				label: __("Storage vCPUs"),
				reqd: 1,
				default: 4,
			},
			{
				fieldname: "storage_ram_gb",
				fieldtype: "Int",
				label: __("Storage Memory (GB)"),
				reqd: 1,
				default: 8,
			},
			{
				fieldname: "storage_disk_gb",
				fieldtype: "Int",
				label: __("Storage Disk (GB)"),
				reqd: 1,
				default: 500,
				description: __("Garage weights each node by this size."),
			},
			{ fieldtype: "Section Break" },
			{
				fieldname: "server_ip_address",
				fieldtype: "Link",
				label: __("Public IPv4 Address"),
				options: "Metal Server IP Address",
				reqd: 1,
				filters: {
					status: "Allocated",
					tenant_id: ["in", [-1, 0]],
					virtual_machine: ["is", "not set"],
				},
			},
		],
		primary_action_label: __("Provision"),
		primary_action(values) {
			frm.call({
				method: "provision",
				doc: frm.doc,
				args: { request: { ...values, storage_cluster: storageClusterConfig(values) } },
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
