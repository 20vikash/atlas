function firewallRuleFields() {
	return [
		{
			fieldname: "direction",
			fieldtype: "Select",
			label: __("Direction"),
			options: "inbound\noutbound",
			reqd: 1,
			in_list_view: 1,
		},
		{
			fieldname: "protocol",
			fieldtype: "Select",
			label: __("Protocol"),
			options: "any\ntcp\nudp\nicmp",
			reqd: 1,
			in_list_view: 1,
		},
		{
			fieldname: "ports",
			fieldtype: "Data",
			label: __("Ports"),
			description: __("Use one port or one range, such as 22 or 8000-9000."),
			in_list_view: 1,
		},
		{
			fieldname: "cidrs",
			fieldtype: "Data",
			label: __("CIDRs"),
			reqd: 1,
			description: __("Separate IPv4 and IPv6 prefixes with commas or spaces."),
			in_list_view: 1,
		},
	];
}

function firewallValue(enabled, rows) {
	const firewall = { enabled: Boolean(enabled), inbound: [], outbound: [] };
	(rows || []).forEach((row) => {
		if (!row.direction && !row.protocol && !row.cidrs) return;
		if (!["inbound", "outbound"].includes(row.direction)) {
			frappe.throw(__("Each firewall rule needs a direction."));
		}
		firewall[row.direction].push({
			protocol: row.protocol,
			ports: (row.ports || "").trim(),
			cidrs: (row.cidrs || "")
				.split(/[\s,]+/)
				.map((cidr) => cidr.trim())
				.filter(Boolean),
		});
	});
	return firewall;
}

function showCreateVirtualMachineDialog() {
	const dialog = new frappe.ui.Dialog({
		title: __("Create Virtual Machine"),
		size: "large",
		fields: [
			{ fieldtype: "Section Break", label: __("Machine") },
			{
				fieldname: "virtual_machine_image",
				fieldtype: "Link",
				label: __("Virtual Machine Image"),
				options: "Virtual Machine Image",
				reqd: 1,
				filters: { enabled: 1, status: "Available" },
			},
			{
				fieldname: "cpu_millicores",
				fieldtype: "Int",
				label: __("CPU (millicores)"),
				reqd: 1,
				min: 100,
				max: 32000,
				default: 1000,
				description: __(
					"The valid range is 100 to 32000 millicores. 1000 millicores equals one CPU core."
				),
				show_description_on_click: 1,
			},
			{
				fieldname: "disk_throughput_mibps",
				fieldtype: "Int",
				label: __("Disk Throughput (MiB/s)"),
				default: 0,
				description: __("0 does not apply a limit."),
			},
			{ fieldtype: "Column Break" },
			{
				fieldname: "memory_mib",
				fieldtype: "Int",
				label: __("Memory (MiB)"),
				reqd: 1,
				default: 1024,
			},
			{
				fieldname: "disk_mib",
				fieldtype: "Int",
				label: __("Disk (MiB)"),
				reqd: 1,
				default: 10240,
			},
			{
				fieldname: "disk_iops",
				fieldtype: "Int",
				label: __("Disk IOPS"),
				default: 0,
				description: __("0 does not apply a limit."),
			},
			{ fieldtype: "Section Break", label: __("Network") },
			{
				fieldname: "egress",
				fieldtype: "Select",
				label: __("Egress"),
				options: "uplink\nmesh\nnone",
				default: "uplink",
				reqd: 1,
				description: __("uplink reaches the internet. mesh reaches tenant VMs only."),
			},
			{
				fieldname: "tenant_id",
				fieldtype: "Int",
				label: __("Tenant ID"),
				description: __("Same tenant connects through the mesh. 0 is infrastructure."),
				default: 0,
			},
			{
				fieldname: "is_privileged",
				fieldtype: "Check",
				label: __("Privileged"),
				default: 0,
				description: __("Reaches every tenant. Needs tenant 0."),
			},
			{ fieldtype: "Column Break" },
			{
				fieldname: "private_network_throughput_mibps",
				fieldtype: "Int",
				label: __("Private Network Throughput (MiB/s)"),
				default: 0,
				description: __("0 does not apply a limit."),
			},
			{
				fieldname: "public_network_throughput_mibps",
				fieldtype: "Int",
				label: __("Public Network Throughput (MiB/s)"),
				default: 0,
				description: __("0 does not apply a limit."),
			},
			{
				fieldname: "server_ip_address",
				fieldtype: "Link",
				label: __("Public IPv4"),
				options: "Metal Server IP Address",
				depends_on: 'eval:doc.egress == "uplink"',
				filters: { status: "Allocated" },
			},
			{ fieldtype: "Section Break", label: __("Firewall") },
			{
				fieldname: "firewall_enabled",
				fieldtype: "Check",
				label: __("Enabled"),
				default: 0,
				description: __("When enabled, unmatched new traffic is blocked."),
			},
			{
				fieldname: "firewall_rules",
				fieldtype: "Table",
				label: __("Allow Rules"),
				in_place_edit: true,
				data: [],
				fields: firewallRuleFields(),
			},
			{ fieldtype: "Section Break", label: __("Guest") },
			{ fieldname: "hostname", fieldtype: "Data", label: __("Hostname") },
			{
				fieldname: "ssh_keys",
				fieldtype: "Code",
				label: __("SSH Keys"),
				description: __("One public key per line."),
			},
			{ fieldtype: "Column Break" },
			{
				fieldname: "user_data",
				fieldtype: "Code",
				label: __("User Data"),
				options: "YAML",
			},
		],
		primary_action_label: __("Create"),
		primary_action(values) {
			values.firewall = firewallValue(values.firewall_enabled, values.firewall_rules);
			delete values.firewall_enabled;
			delete values.firewall_rules;
			frappe.call({
				method: "atlas.vm.doctype.virtual_machine.virtual_machine.create",
				args: { request: values },
				freeze: true,
				freeze_message: __("Sending Virtual Machine request"),
				callback(response) {
					dialog.hide();
					if (response.message.is_draft) {
						frappe.show_alert({
							message: __(
								"Metal did not confirm the request. Atlas kept the draft."
							),
							indicator: "orange",
						});
					}
					frappe.set_route("Form", "Virtual Machine", response.message.name);
				},
			});
		},
	});
	dialog.show();
}

frappe.listview_settings["Virtual Machine"] = {
	refresh(listview) {
		listview.page.clear_primary_action();
		if (!has_common(frappe.user_roles, ["System Manager"])) return;
		listview.page.add_inner_button(
			__("Create Virtual Machine"),
			showCreateVirtualMachineDialog
		);
	},
};
