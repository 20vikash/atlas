#!/usr/bin/env python3

import base64
import ipaddress
import socket
import struct
import subprocess
import threading
import os

from pyroute2 import IPRoute

from scapy.all import IPv6, ICMPv6ND_NS, ICMPv6ND_NA
from netfilterqueue import NetfilterQueue


# ============================================================
# Configuration
# ============================================================

INTERFACE = os.environ.get("ATLAS_INTERFACE", "eno1.2438")
PUBLIC_INTERFACE = "eno1"

WG_INTERFACE = "wg0"
WG_LISTEN_PORT = 51820

# OUTPUT -> 42
# INPUT  -> 43
NFQUEUE_OUTPUT = 42
NFQUEUE_INPUT = 43

ATLAS_OPTION_TYPE = 253

# Atlas option:
#
#   Type          1 byte
#   Length        1 byte
#   WG pubkey    32 bytes
#   IPv4          4 bytes
#   UDP port      2 bytes
#
# Total = 40 bytes = 5 * 8 bytes
#
ATLAS_OPTION_LENGTH = 5
ATLAS_OPTION_SIZE = 40


# ============================================================
# Shared state
# ============================================================

# VM IPv6 -> Atlas discovery information
#
# {
#     "wg_public_key": "...",
#     "endpoint_ip": "...",
#     "endpoint_port": 51820,
# }
#
discovery_state = {}

state_lock = threading.RLock()


# ============================================================
# Helpers
# ============================================================

def run_command(command):

    return subprocess.run(
        command,
        check=True,
        text=True,
        capture_output=True,
    )


# ============================================================
# WireGuard information
# ============================================================

def get_wg_public_key():

    try:

        result = run_command(
            [
                "wg",
                "show",
                WG_INTERFACE,
                "public-key",
            ]
        )

        return result.stdout.strip()

    except subprocess.CalledProcessError:

        return None


def get_public_ipv4():

    try:

        result = run_command(
            [
                "ip",
                "-4",
                "-o",
                "addr",
                "show",
                "dev",
                PUBLIC_INTERFACE,
            ]
        )

    except subprocess.CalledProcessError:

        return None

    for line in result.stdout.splitlines():

        parts = line.split()

        try:

            index = parts.index("inet")

            return parts[index + 1].split("/")[0]

        except (ValueError, IndexError):

            continue

    return None


# ============================================================
# WireGuard AllowedIPs
# ============================================================

def get_peer_allowed_ips(peer_key):

    try:

        result = run_command(
            [
                "wg",
                "show",
                WG_INTERFACE,
                "allowed-ips",
            ]
        )

    except subprocess.CalledProcessError:

        return []

    for line in result.stdout.splitlines():

        parts = line.split()

        if len(parts) < 2:

            continue

        if parts[0] == peer_key:

            # `wg show ... allowed-ips` prints `(none)` when this
            # peer currently has no AllowedIPs. Treat it as empty.
            if parts[1] == "(none)":
                return []

            return parts[1].split(",")

    return []


# ============================================================
# Managed IPv6 neighbors
# ============================================================

def add_managed_neighbor(ipv6):
    """Create a kernel-managed IPv6 neighbor for a remote VM."""
    try:
        run_command([
            "ip", "-6", "neigh", "replace",
            ipv6, "dev", INTERFACE, "managed"
        ])
        print(f"[NEIGH] Managed neighbor: {ipv6} dev {INTERFACE}")
    except subprocess.CalledProcessError as e:
        print(
            "[ERROR] Managed neighbor creation failed: "
            f"{e.stderr.strip()}"
        )


# ============================================================
# Routes
# ============================================================

def add_route(ipv6):

    route = f"{ipv6}/128"

    try:

        run_command(
            [
                "ip",
                "-6",
                "route",
                "replace",
                route,
                "dev",
                WG_INTERFACE,
            ]
        )

        print(
            f"[ROUTE] {route} -> {WG_INTERFACE}"
        )

    except subprocess.CalledProcessError as e:

        print(
            "[ERROR] Route add failed: "
            f"{e.stderr.strip()}"
        )


def remove_route(ipv6):

    route = f"{ipv6}/128"

    subprocess.run(
        [
            "ip",
            "-6",
            "route",
            "del",
            route,
            "dev",
            WG_INTERFACE,
        ],
        check=False,
        capture_output=True,
        text=True,
    )

    print(
        f"[ROUTE] Removed {route}"
    )


# ============================================================
# WireGuard peer configuration
# ============================================================

def configure_peer(
    peer_key,
    endpoint_ip,
    endpoint_port,
    allowed_ip,
):

    allowed_ip = f"{allowed_ip}/128"

    existing = get_peer_allowed_ips(
        peer_key
    )

    if allowed_ip not in existing:

        existing.append(
            allowed_ip
        )

    print()
    print("[WIREGUARD]")
    print(f"    Peer       : {peer_key}")
    print(
        f"    Endpoint   : "
        f"{endpoint_ip}:{endpoint_port}"
    )
    print(
        f"    Allowed IP : {allowed_ip}"
    )

    try:

        run_command(
            [
                "wg",
                "set",
                WG_INTERFACE,
                "peer",
                peer_key,
                "endpoint",
                f"{endpoint_ip}:{endpoint_port}",
                "allowed-ips",
                ",".join(existing),
            ]
        )

        print(
            "    [WG] Peer configured."
        )

        add_route(
            allowed_ip.split("/")[0]
        )

        return True

    except subprocess.CalledProcessError as e:

        print(
            "[ERROR] WireGuard configuration failed:"
        )

        print(
            f"        {e.stderr.strip()}"
        )

        return False


def remove_allowed_ip(
    peer_key,
    ipv6,
):

    target = f"{ipv6}/128"

    existing = get_peer_allowed_ips(
        peer_key
    )

    remaining = [
        ip
        for ip in existing
        if ip != target
    ]

    print()
    print("[WIREGUARD]")
    print(f"    Peer     : {peer_key}")
    print(f"    Removing : {target}")

    try:

        if remaining:

            run_command(
                [
                    "wg",
                    "set",
                    WG_INTERFACE,
                    "peer",
                    peer_key,
                    "allowed-ips",
                    ",".join(remaining),
                ]
            )

        else:

            run_command(
                [
                    "wg",
                    "set",
                    WG_INTERFACE,
                    "peer",
                    peer_key,
                    "remove",
                ]
            )

            print(
                "    [WG] Peer removed."
            )

        remove_route(
            ipv6
        )

    except subprocess.CalledProcessError as e:

        print(
            "[ERROR] WireGuard removal failed:"
        )

        print(
            f"        {e.stderr.strip()}"
        )


# ============================================================
# Atlas option
# ============================================================

def build_atlas_option():

    wg_key = get_wg_public_key()
    endpoint_ip = get_public_ipv4()

    if not wg_key:

        print(
            "[ERROR] wg0 has no public key."
        )

        return None

    if not endpoint_ip:

        print(
            "[ERROR] Could not determine "
            "public IPv4."
        )

        return None

    try:

        key_bytes = base64.b64decode(
            wg_key,
            validate=True,
        )

        if len(key_bytes) != 32:

            raise ValueError(
                "WG public key is not 32 bytes"
            )

        endpoint_bytes = socket.inet_aton(
            endpoint_ip
        )

    except (ValueError, OSError) as e:

        print(
            f"[ERROR] Atlas option build failed: {e}"
        )

        return None

    option = (
        bytes(
            [
                ATLAS_OPTION_TYPE,
                ATLAS_OPTION_LENGTH,
            ]
        )
        + key_bytes
        + endpoint_bytes
        + struct.pack(
            "!H",
            WG_LISTEN_PORT,
        )
    )

    if len(option) != ATLAS_OPTION_SIZE:

        print(
            "[ERROR] Atlas option size is "
            f"{len(option)}, expected "
            f"{ATLAS_OPTION_SIZE}"
        )

        return None

    return option


# ============================================================
# Find Atlas option in raw NDP options
# ============================================================

def find_atlas_option(options):

    offset = 0

    while offset + 2 <= len(options):

        option_type = options[offset]
        length_units = options[offset + 1]

        # Length 0 is invalid and would otherwise
        # cause an infinite loop.
        if length_units == 0:

            break

        option_size = length_units * 8

        if (
            offset + option_size
            > len(options)
        ):

            break

        option = options[
            offset:
            offset + option_size
        ]

        if (
            option_type
            == ATLAS_OPTION_TYPE
        ):

            if len(option) < ATLAS_OPTION_SIZE:

                return None

            if (
                option[1]
                != ATLAS_OPTION_LENGTH
            ):

                return None

            return option[:ATLAS_OPTION_SIZE]

        offset += option_size

    return None


# ============================================================
# Extract raw NDP options
# ============================================================

def get_ndp_options(packet):

    """
    NS and NA have a fixed ICMPv6 header followed
    by zero or more NDP options.

    We use Scapy only to locate the NS/NA layer,
    then operate on the raw bytes.
    """

    raw = bytes(packet)

    if packet.haslayer(
        ICMPv6ND_NS
    ):

        layer = packet[
            ICMPv6ND_NS
        ]

        offset = (
            raw.find(
                bytes(layer)
            )
        )

        if offset < 0:

            return b""

        fixed_header_size = 24

        return raw[
            offset + fixed_header_size:
        ]

    if packet.haslayer(
        ICMPv6ND_NA
    ):

        layer = packet[
            ICMPv6ND_NA
        ]

        offset = (
            raw.find(
                bytes(layer)
            )
        )

        if offset < 0:

            return b""

        fixed_header_size = 24

        return raw[
            offset + fixed_header_size:
        ]

    return b""


# ============================================================
# Parse Atlas option
# ============================================================

def parse_atlas_option(option):

    if option is None:

        return None

    if len(option) != ATLAS_OPTION_SIZE:

        return None

    if option[0] != ATLAS_OPTION_TYPE:

        return None

    if option[1] != ATLAS_OPTION_LENGTH:

        return None

    key_bytes = option[
        2:34
    ]

    endpoint_bytes = option[
        34:38
    ]

    port = struct.unpack(
        "!H",
        option[38:40],
    )[0]

    try:

        endpoint_ip = socket.inet_ntoa(
            endpoint_bytes
        )

    except OSError:

        return None

    return {
        "wg_public_key":
            base64.b64encode(
                key_bytes
            ).decode(),

        "endpoint_ip":
            endpoint_ip,

        "endpoint_port":
            port,
    }


# ============================================================
# Add Atlas option to NDP packet
# ============================================================

def append_atlas_option(
    packet,
    atlas_option,
):

    raw = bytes(packet)

    raw += atlas_option

    # IPv6 payload length.
    #
    # IPv6 header:
    #   bytes 4..5 = payload length
    #
    payload_length = (
        struct.unpack(
            "!H",
            raw[4:6],
        )[0]
        + len(atlas_option)
    )

    raw = (
        raw[:4]
        + struct.pack(
            "!H",
            payload_length,
        )
        + raw[6:]
    )

    # ICMPv6 checksum needs to be
    # recalculated because we changed
    # the payload.
    #
    # Scapy is used here only to
    # recalculate it.
    #

    rebuilt = IPv6(raw)

    if rebuilt.haslayer(
        ICMPv6ND_NS
    ):

        icmp = rebuilt[
            ICMPv6ND_NS
        ]

        if hasattr(
            icmp,
            "cksum",
        ):

            del icmp.cksum

    if rebuilt.haslayer(
        ICMPv6ND_NA
    ):

        icmp = rebuilt[
            ICMPv6ND_NA
        ]

        if hasattr(
            icmp,
            "cksum",
        ):

            del icmp.cksum

    return bytes(
        rebuilt
    )


# ============================================================
# Local VM discovery
# ============================================================

def get_local_vm_ipv6_addresses():

    addresses = set()

    try:

        output = subprocess.check_output(
            [
                "ip",
                "netns",
                "list",
            ],
            text=True,
        )

    except subprocess.CalledProcessError:

        return addresses

    for line in output.splitlines():

        if not line.strip():

            continue

        namespace = line.split()[0]

        try:

            output = subprocess.check_output(
                [
                    "ip",
                    "netns",
                    "exec",
                    namespace,
                    "ip",
                    "-6",
                    "-o",
                    "addr",
                    "show",
                ],
                text=True,
            )

        except subprocess.CalledProcessError:

            continue

        for addr_line in output.splitlines():

            parts = addr_line.split()

            try:

                index = parts.index(
                    "inet6"
                )

                address = (
                    parts[index + 1]
                    .split("/")[0]
                )

            except (
                ValueError,
                IndexError,
            ):

                continue

            if address.startswith(
                "fe80:"
            ):

                continue

            addresses.add(
                address
            )

    return addresses


def owns_vm_ipv6(address):

    return (
        address
        in get_local_vm_ipv6_addresses()
    )


# ============================================================
# OUTGOING NS
#
# Add our WG information.
# ============================================================

def process_outgoing_ns(
    nfpacket,
    packet,
):

    ns = packet[
        ICMPv6ND_NS
    ]

    print()
    print(
        "=== Outgoing Neighbor Solicitation ==="
    )

    print(
        f"Source IPv6 : {packet.src}"
    )

    print(
        f"Target IPv6 : {ns.tgt}"
    )

    # DAD uses unspecified source.
    if packet.src == "::":

        print(
            "[INFO] DAD NS. Leaving unchanged."
        )

        nfpacket.accept()

        return

    options = get_ndp_options(
        packet
    )

    if find_atlas_option(
        options
    ):

        print(
            "[INFO] Atlas option already present."
        )

        nfpacket.accept()

        return

    atlas_option = build_atlas_option()

    if atlas_option is None:

        nfpacket.accept()

        return

    modified = append_atlas_option(
        packet,
        atlas_option,
    )

    nfpacket.set_payload(
        modified
    )

    print(
        ">>> Added Atlas option to NS"
    )

    print(
        f"    WG pubkey : "
        f"{get_wg_public_key()}"
    )

    print(
        f"    Endpoint  : "
        f"{get_public_ipv4()}:"
        f"{WG_LISTEN_PORT}"
    )

    nfpacket.accept()


# ============================================================
# INCOMING NS
#
# Learn the sender's WG information.
# ============================================================

def process_incoming_ns(
    nfpacket,
    packet,
):

    ns = packet[
        ICMPv6ND_NS
    ]

    print()
    print(
        "=== Incoming Neighbor Solicitation ==="
    )

    print(
        f"Source IPv6 : {packet.src}"
    )

    print(
        f"Target IPv6 : {ns.tgt}"
    )

    if packet.src == "::":

        print(
            "[INFO] DAD NS."
        )

        nfpacket.accept()

        return

    options = get_ndp_options(
        packet
    )

    option = find_atlas_option(
        options
    )

    if option is None:

        print(
            "[INFO] No Atlas option."
        )

        nfpacket.accept()

        return

    information = parse_atlas_option(
        option
    )

    if information is None:

        print(
            "[ERROR] Invalid Atlas option."
        )

        nfpacket.accept()

        return

    try:
        source_ip = ipaddress.IPv6Address(packet.src)

    except ValueError:

        nfpacket.accept()

        return

    # Node link-local addresses are not VM addresses. Never install
    # fe80::/10 as a VM WireGuard AllowedIP or /128 route.
    if source_ip.is_link_local:

        print(
            "[INFO] Link-local NS source. "
            "Not adding it as a VM AllowedIP."
        )

        nfpacket.accept()

        return

    with state_lock:

        configure_peer(
            information[
                "wg_public_key"
            ],
            information[
                "endpoint_ip"
            ],
            information[
                "endpoint_port"
            ],
            packet.src,
        )

    print()
    print(
        "[DISCOVERY] Learned remote node "
        "from NS"
    )

    print(
        f"    Source   : {packet.src}"
    )

    print(
        f"    WG peer  : "
        f"{information['wg_public_key']}"
    )

    print(
        f"    Endpoint : "
        f"{information['endpoint_ip']}:"
        f"{information['endpoint_port']}"
    )

    nfpacket.accept()


# ============================================================
# OUTGOING NA
#
# If the NA is for our VM, advertise
# our WG information.
# ============================================================

def process_outgoing_na(
    nfpacket,
    packet,
):

    na = packet[
        ICMPv6ND_NA
    ]

    print()
    print(
        "=== Outgoing Neighbor Advertisement ==="
    )

    print(
        f"Source IPv6 : {packet.src}"
    )

    print(
        f"Target IPv6 : {na.tgt}"
    )

    if not owns_vm_ipv6(
        na.tgt
    ):

        print(
            "[INFO] Target VM is not hosted "
            "on this node."
        )

        nfpacket.accept()

        return

    print(
        "[INFO] Target VM is hosted "
        "on this node."
    )

    options = get_ndp_options(
        packet
    )

    if find_atlas_option(
        options
    ):

        print(
            "[INFO] Atlas option already present."
        )

        nfpacket.accept()

        return

    atlas_option = build_atlas_option()

    if atlas_option is None:

        nfpacket.accept()

        return

    modified = append_atlas_option(
        packet,
        atlas_option,
    )

    nfpacket.set_payload(
        modified
    )

    print()
    print(
        ">>> Added Atlas option to NA"
    )

    print(
        f"    WG pubkey : "
        f"{get_wg_public_key()}"
    )

    print(
        f"    Endpoint  : "
        f"{get_public_ipv4()}:"
        f"{WG_LISTEN_PORT}"
    )

    nfpacket.accept()


# ============================================================
# INCOMING NA
#
# Learn remote VM -> WG peer.
# ============================================================

def process_incoming_na(
    nfpacket,
    packet,
):

    na = packet[
        ICMPv6ND_NA
    ]

    print()
    print(
        "=== Incoming Neighbor Advertisement ==="
    )

    print(
        f"Source IPv6 : {packet.src}"
    )

    print(
        f"Target IPv6 : {na.tgt}"
    )

    if owns_vm_ipv6(
        na.tgt
    ):

        print(
            "[INFO] NA is for our local VM."
        )

        nfpacket.accept()

        return

    options = get_ndp_options(
        packet
    )

    option = find_atlas_option(
        options
    )

    if option is None:

        print(
            "[INFO] No Atlas option."
        )

        nfpacket.accept()

        return

    information = parse_atlas_option(
        option
    )

    if information is None:

        print(
            "[ERROR] Invalid Atlas option."
        )

        nfpacket.accept()

        return

    remote_vm = na.tgt

    with state_lock:

        success = configure_peer(
            information[
                "wg_public_key"
            ],
            information[
                "endpoint_ip"
            ],
            information[
                "endpoint_port"
            ],
            remote_vm,
        )

        if success:
            # Keep the VM represented on the private-network interface so
            # Linux NUD can independently monitor its reachability.
            add_managed_neighbor(remote_vm)

            discovery_state[
                remote_vm
            ] = information
        else:
            print(
                "[DISCOVERY] Remote VM learned, but WireGuard "
                "configuration failed. Not committing discovery state."
            )

    print()
    print(
        "[DISCOVERY] Learned remote VM"
    )

    print(
        f"    VM       : {remote_vm}"
    )

    print(
        f"    WG peer  : "
        f"{information['wg_public_key']}"
    )

    print(
        f"    Endpoint : "
        f"{information['endpoint_ip']}:"
        f"{information['endpoint_port']}"
    )

    nfpacket.accept()


# ============================================================
# NFQUEUE callbacks
# ============================================================

def process_output_packet(
    nfpacket
):

    try:

        packet = IPv6(
            nfpacket.get_payload()
        )

    except Exception as e:

        print(
            f"[ERROR] OUTPUT parse failed: {e}"
        )

        nfpacket.accept()

        return

    if packet.haslayer(
        ICMPv6ND_NS
    ):

        process_outgoing_ns(
            nfpacket,
            packet,
        )

        return

    if packet.haslayer(
        ICMPv6ND_NA
    ):

        process_outgoing_na(
            nfpacket,
            packet,
        )

        return

    nfpacket.accept()


def process_input_packet(
    nfpacket
):

    try:

        packet = IPv6(
            nfpacket.get_payload()
        )

    except Exception as e:

        print(
            f"[ERROR] INPUT parse failed: {e}"
        )

        nfpacket.accept()

        return

    if packet.haslayer(
        ICMPv6ND_NS
    ):

        process_incoming_ns(
            nfpacket,
            packet,
        )

        return

    if packet.haslayer(
        ICMPv6ND_NA
    ):

        process_incoming_na(
            nfpacket,
            packet,
        )

        return

    nfpacket.accept()


# ============================================================
# NFQUEUE workers
# ============================================================

def output_nfqueue_worker():

    nfqueue = NetfilterQueue()

    nfqueue.bind(
        NFQUEUE_OUTPUT,
        process_output_packet,
    )

    print(
        "[NDP] OUTPUT NFQUEUE "
        f"{NFQUEUE_OUTPUT} started."
    )

    try:

        nfqueue.run()

    except KeyboardInterrupt:

        pass

    finally:

        nfqueue.unbind()


def input_nfqueue_worker():

    nfqueue = NetfilterQueue()

    nfqueue.bind(
        NFQUEUE_INPUT,
        process_input_packet,
    )

    print(
        "[NDP] INPUT NFQUEUE "
        f"{NFQUEUE_INPUT} started."
    )

    try:

        nfqueue.run()

    except KeyboardInterrupt:

        pass

    finally:

        nfqueue.unbind()


# ============================================================
# NUD
# ============================================================

NUD_STATES = {
    0x01: "INCOMPLETE",
    0x02: "REACHABLE",
    0x04: "STALE",
    0x08: "DELAY",
    0x10: "PROBE",
    0x20: "FAILED",
    0x40: "NOARP",
    0x80: "PERMANENT",
}


def state_name(
    state
):

    names = []

    for bit, name in NUD_STATES.items():

        if state & bit:

            names.append(
                name
            )

    return (
        "|".join(names)
        if names
        else f"UNKNOWN({state:#x})"
    )


# ============================================================
# NUD monitor
# ============================================================

def nud_monitor():

    ipr = IPRoute()

    print(
        "[NUD] Listening for kernel "
        "neighbor events..."
    )

    try:

        ipr.bind(
            groups=1 << 2
        )

        while True:

            messages = ipr.get()

            for msg in messages:

                if msg["event"] not in (
                    "RTM_NEWNEIGH",
                    "RTM_DELNEIGH",
                ):

                    continue

                if (
                    msg.get("family")
                    != socket.AF_INET6
                ):

                    continue

                attrs = dict(
                    msg.get(
                        "attrs",
                        []
                    )
                )

                ipv6 = attrs.get(
                    "NDA_DST"
                )

                if not ipv6:

                    continue

                ifindex = msg.get(
                    "ifindex"
                )

                links = ipr.get_links(
                    ifindex
                )

                if not links:

                    continue

                interface = (
                    links[0].get_attr(
                        "IFLA_IFNAME"
                    )
                )

                if interface != INTERFACE:

                    continue

                mac = attrs.get(
                    "NDA_LLADDR",
                    "-",
                )

                state = msg.get(
                    "state",
                    0,
                )

                print()
                print(
                    "=== NUD EVENT ==="
                )

                print(
                    f"IPv6  : {ipv6}"
                )

                print(
                    f"Dev   : {interface}"
                )

                print(
                    f"MAC   : {mac}"
                )

                print(
                    f"State : "
                    f"{state_name(state)}"
                )

                # Only FAILED triggers cleanup.
                if not (
                    state & 0x20
                ):

                    continue

                with state_lock:

                    information = (
                        discovery_state.get(
                            ipv6
                        )
                    )

                    if information is None:

                        print(
                            "[ATLAS] FAILED neighbor "
                            "has no discovery state."
                        )

                        continue

                    peer_key = (
                        information[
                            "wg_public_key"
                        ]
                    )

                    print()
                    print(
                        "[ATLAS] VM became "
                        "unreachable!"
                    )

                    print(
                        f"    VM      : {ipv6}"
                    )

                    print(
                        f"    WG peer : {peer_key}"
                    )

                    remove_allowed_ip(
                        peer_key,
                        ipv6,
                    )

                    # Keep the managed neighbor entry. The kernel must
                    # continue NUD/managed-neighbor maintenance so that
                    # the VM can be rediscovered if ownership moves.
                    print(
                        f"[NEIGH] Keeping managed neighbor: "
                        f"{ipv6} dev {INTERFACE}"
                    )

                    discovery_state.pop(
                        ipv6,
                        None,
                    )

                    print(
                        "[ATLAS] Discovery state removed."
                    )

    except KeyboardInterrupt:

        print(
            "\n[NUD] Stopping monitor."
        )

    finally:

        ipr.close()


# ============================================================
# Main
# ============================================================

def main():

    print()
    print(
        "=========================================="
    )
    print(
        "       ATLAS USERSPACE DAEMON"
    )
    print(
        "=========================================="
    )

    print(
        f"VLAN interface : {INTERFACE}"
    )

    print(
        f"Public iface   : {PUBLIC_INTERFACE}"
    )

    print(
        f"WireGuard      : {WG_INTERFACE}"
    )

    print(
        f"WG port        : {WG_LISTEN_PORT}"
    )

    print(
        f"OUTPUT queue   : {NFQUEUE_OUTPUT}"
    )

    print(
        f"INPUT queue    : {NFQUEUE_INPUT}"
    )

    print()

    print(
        f"WG public key  : "
        f"{get_wg_public_key()}"
    )

    print(
        f"WG endpoint    : "
        f"{get_public_ipv4()}:"
        f"{WG_LISTEN_PORT}"
    )

    print()

    print(
        "Discovery:"
    )

    print(
        "  NS → our WG info"
    )

    print(
        "  NA → remote WG info"
    )

    print()

    print(
        "Liveness:"
    )

    print(
        "  RTNETLINK NUD → FAILED"
    )

    print()

    print(
        "WireGuard:"
    )

    print(
        "  NDP → add peer + /128"
    )

    print(
        "  FAILED → remove /128"
    )

    print()

    # NUD
    nud_thread = threading.Thread(
        target=nud_monitor,
        daemon=True,
    )

    nud_thread.start()

    # INPUT NFQUEUE
    input_thread = threading.Thread(
        target=input_nfqueue_worker,
        daemon=True,
    )

    input_thread.start()

    # OUTPUT NFQUEUE runs in main thread.
    output_nfqueue_worker()


if __name__ == "__main__":

    main()


# Managed neighbors are created automatically when a remote VM is discovered
# through an incoming NA. They are kept after NUD FAILED so the kernel can
# continue probing and detect the VM if ownership returns or moves.
