// Copyright (c) 2026, Frappe and contributors
// For license information, please see license.txt

function storageClusterConfig(values) {
	return {
		storage_node_count: values.storage_node_count,
		replication_factor: values.replication_factor,
		gateway: {
			cpu_millicores: values.gateway_cpu_millicores,
			ram_gb: values.gateway_ram_gb,
			disk_gb: values.gateway_disk_gb,
		},
		storage: {
			cpu_millicores: values.storage_cpu_millicores,
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
			{
				fieldname: "cpu_millicores",
				fieldtype: "Int",
				label: __("CPU (millicores)"),
				description: __("1000 millicores equals one CPU core."),
				reqd: 1,
				default: 2000,
			},
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
				fieldname: "gateway_cpu_millicores",
				fieldtype: "Int",
				label: __("Gateway CPU (millicores)"),
				description: __("1000 millicores equals one CPU core."),
				reqd: 1,
				default: 2000,
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
				fieldname: "storage_cpu_millicores",
				fieldtype: "Int",
				label: __("Storage CPU (millicores)"),
				description: __("1000 millicores equals one CPU core."),
				reqd: 1,
				default: 4000,
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

function showPilotAdminPassword(response) {
	const dialog = new frappe.ui.Dialog({
		title: __("New Pilot Admin Password"),
		fields: [
			{
				fieldname: "domain",
				fieldtype: "Data",
				label: __("Admin Panel"),
				read_only: 1,
				default: `https://${response.domain}`,
			},
			{
				fieldname: "password",
				fieldtype: "Data",
				label: __("Password"),
				read_only: 1,
				default: response.password,
			},
			{
				fieldtype: "HTML",
				options: `<p class="text-muted small">${__(
					"Atlas shows this password once."
				)}</p>`,
			},
		],
		primary_action_label: __("Copy Password"),
		primary_action() {
			frappe.utils.copy_to_clipboard(response.password);
			dialog.hide();
		},
	});
	dialog.show();
}

function setPilotReleaseTracker(frm, method, enabled) {
	const action = enabled ? __("enable") : __("disable");
	frappe.confirm(__("{0} automatic Pilot image builds?", [action]), () =>
		frm
			.call({
				method,
				doc: frm.doc,
				freeze: true,
				freeze_message: enabled
					? __("Enabling automatic Pilot image builds...")
					: __("Disabling automatic Pilot image builds..."),
			})
			.then(() => frm.refresh())
	);
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

		if (frm.doc.status === "Active") {
			frm.add_custom_button(
				__("Reset Pilot Admin Password"),
				() =>
					frappe.confirm(
						__("Reset the Pilot administration password on the Cargo host?"),
						() =>
							frm
								.call({
									method: "reset_pilot_admin_password",
									doc: frm.doc,
									freeze: true,
									freeze_message: __("Resetting the Pilot admin password..."),
								})
								.then((response) => showPilotAdminPassword(response.message))
					),
				__("Actions")
			);

			frm.add_custom_button(
				frm.doc.auto_build_pilot_images
					? __("Disable Auto Build Pilot Images")
					: __("Enable Auto Build Pilot Images"),
				() =>
					setPilotReleaseTracker(
						frm,
						frm.doc.auto_build_pilot_images
							? "disable_pilot_release_tracker"
							: "enable_pilot_release_tracker",
						!frm.doc.auto_build_pilot_images
					),
				__("Actions")
			);
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
