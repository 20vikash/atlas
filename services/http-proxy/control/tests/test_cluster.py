import asyncio
import time
from dataclasses import replace
from unittest.mock import AsyncMock

import httpx
import pytest
from fastapi import HTTPException

from proxy_control.cluster import (
	FORWARDED_WRITE_TIMEOUT_SECONDS,
	PEER_CALL_ATTEMPTS,
	PEER_REPAIR_CALLS,
	PEER_TIMEOUT_SECONDS,
	ClusterManager,
	ClusterSnapshot,
	HeartbeatRequest,
	Mutation,
	SnapshotStore,
	VoteRequest,
)
from proxy_control.config import ClusterConfig, ClusterPeer


class _MemoryMappings:
	"""Store maps without OpenResty for cluster unit tests."""

	def __init__(self):
		self.values = {"sites": {}, "domains": {}}

	async def get(self, kind):
		return dict(self.values[kind])

	async def replace(self, kind, values):
		self.values[kind] = dict(values)
		return {"synced": True, "entries": len(values)}

	async def update(self, kind, key, address):
		self.values[kind][key] = address
		return {"site" if kind == "sites" else "domain": key, "address": address}

	async def delete(self, kind, key):
		self.values[kind].pop(key, None)

	def without_reserved(self, _kind, values):
		return values


def _configuration(tmp_path, member_count=1):
	peers = tuple(
		ClusterPeer(node_id=f"proxy-{index:03}", address=f"https://proxy-{index:03}.example.com")
		for index in range(1, member_count + 1)
	)
	return ClusterConfig(
		node_id="proxy-001",
		password="current-secret",
		previous_password="previous-secret",
		previous_password_valid_until=int(time.time()) + 600,
		state_path=tmp_path / "cluster-state.json",
		peers=peers,
	)


def test_snapshot_store_replaces_complete_state(tmp_path):
	store = SnapshotStore(tmp_path / "state.json")
	snapshot = ClusterSnapshot(generation=7, sites={"erp": "2001:db8::1"})

	store.save(snapshot)

	assert store.load() == snapshot
	assert (tmp_path / "state.json").stat().st_mode & 0o777 == 0o600


@pytest.mark.parametrize(
	("members", "required"),
	[(1, 1), (2, 2), (3, 3), (4, 3), (5, 3)],
)
def test_acknowledgement_policy(tmp_path, members, required):
	manager = ClusterManager(_configuration(tmp_path, members), _MemoryMappings())

	assert manager._required_acknowledgements == required

	asyncio.run(manager.client.aclose())


def test_one_node_mutation_increases_the_generation(tmp_path):
	async def run():
		mappings = _MemoryMappings()
		manager = ClusterManager(_configuration(tmp_path), mappings)
		await manager.start()

		result = await manager.mutate(
			Mutation(kind="sites", action="update", key="erp", address="2001:db8::1")
		)

		assert result.generation == 1
		assert mappings.values["sites"] == {"erp": "2001:db8::1"}
		assert SnapshotStore(tmp_path / "cluster-state.json").load().generation == 1
		await manager.close()

	asyncio.run(run())


def test_four_nodes_accept_one_missing_acknowledgement(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 4), _MemoryMappings())
		manager.role = "leader"
		manager.leader_id = "proxy-001"
		manager.is_initialized = True
		manager.is_synchronized = True
		manager._replicate = AsyncMock(side_effect=[True, True, RuntimeError("offline")])

		result = await manager.mutate(
			Mutation(kind="sites", action="update", key="erp", address="2001:db8::1")
		)

		assert result.generation == 1
		await manager.close()

	asyncio.run(run())


def test_five_nodes_accept_two_missing_acknowledgements(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 5), _MemoryMappings())
		manager.role = "leader"
		manager.leader_id = "proxy-001"
		manager.is_initialized = True
		manager.is_synchronized = True
		manager._replicate = AsyncMock(
			side_effect=[True, True, RuntimeError("offline"), RuntimeError("offline")]
		)

		result = await manager.mutate(
			Mutation(kind="sites", action="update", key="erp", address="2001:db8::1")
		)

		assert result.generation == 1
		await manager.close()

	asyncio.run(run())


def test_three_nodes_reject_one_missing_acknowledgement(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.role = "leader"
		manager.leader_id = "proxy-001"
		manager.is_initialized = True
		manager.is_synchronized = True
		manager._replicate = AsyncMock(side_effect=[True, RuntimeError("offline")])

		with pytest.raises(HTTPException) as raised:
			await manager.mutate(Mutation(kind="sites", action="update", key="erp", address="2001:db8::1"))

		assert raised.value.status_code == 503
		assert raised.value.detail["acknowledged"] == 2
		await manager.close()

	asyncio.run(run())


def test_vote_requires_a_current_generation(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(term=2, generation=8)

		result = await manager.request_vote(VoteRequest(term=3, candidate_id="proxy-002", generation=7))

		assert result == {"term": 3, "granted": False}
		await manager.close()

	asyncio.run(run())


def test_vote_requires_a_current_mutation_term(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(term=5, generation=7, mutation_term=4)

		result = await manager.request_vote(
			VoteRequest(
				term=6,
				candidate_id="proxy-002",
				generation=8,
				mutation_term=3,
			)
		)

		assert result == {"term": 6, "granted": False}
		await manager.close()

	asyncio.run(run())


def test_vote_rejects_a_node_outside_the_configured_membership(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(term=2)

		result = await manager.request_vote(VoteRequest(term=9, candidate_id="proxy-004", generation=9))

		assert result == {"term": 2, "granted": False}
		assert manager.snapshot.term == 2
		await manager.close()

	asyncio.run(run())


def test_candidate_steps_down_for_a_higher_term(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(term=2)
		manager._peer_post = AsyncMock(
			side_effect=[
				{"term": 5, "granted": False},
				{"term": 3, "granted": True},
			]
		)

		await manager._elect()

		assert manager.snapshot.term == 5
		assert manager.role == "follower"
		assert manager.leader_id == ""
		await manager.close()

	asyncio.run(run())


def test_bootstrap_does_not_invent_a_leader(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager._peer_get = AsyncMock(
			side_effect=[
				{"node_id": "proxy-002", "leader_id": "", "generation": 0},
				{"node_id": "proxy-003", "leader_id": "", "generation": 0},
			]
		)

		await manager.bootstrap()

		assert manager.leader_id == ""
		await manager.close()

	asyncio.run(run())


def test_route_snapshot_does_not_replace_current_vote(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(term=8, voted_for="proxy-002", generation=3)

		await manager.install_snapshot(
			ClusterSnapshot(term=7, voted_for="proxy-003", generation=4, sites={"erp": "::1"})
		)

		assert manager.snapshot.term == 8
		assert manager.snapshot.voted_for == "proxy-002"
		assert manager.snapshot.generation == 4
		await manager.close()

	asyncio.run(run())


def test_heartbeat_repairs_a_different_operation_at_the_same_generation(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(term=4, generation=7, operation_id="local-operation")
		manager.is_synchronized = True
		manager._synchronize_from_leader = AsyncMock()

		await manager.heartbeat(
			HeartbeatRequest(
				term=4,
				leader_id="proxy-002",
				generation=7,
				operation_id="leader-operation",
			)
		)
		await asyncio.sleep(0)

		assert not manager.is_synchronized
		manager._synchronize_from_leader.assert_awaited_once()
		await manager.close()

	asyncio.run(run())


def test_elected_leader_snapshot_can_replace_an_uncommitted_generation(tmp_path):
	async def run():
		manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
		manager.snapshot = ClusterSnapshot(generation=8, operation_id="uncommitted")

		await manager.install_snapshot(
			ClusterSnapshot(generation=7, operation_id="committed", sites={"erp": "::1"}),
			allow_older=True,
		)

		assert manager.snapshot.generation == 7
		assert manager.snapshot.operation_id == "committed"
		await manager.close()

	asyncio.run(run())


def test_only_generation_conflicts_trigger_snapshot_repair(tmp_path):
	manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
	request = httpx.Request("POST", "https://proxy-002.example.com/internal/cluster/replicate")
	generation_response = httpx.Response(
		409,
		request=request,
		json={"detail": {"error": "generation mismatch", "generation": 4}},
	)
	leader_response = httpx.Response(
		409,
		request=request,
		json={"detail": {"leader_id": "proxy-003"}},
	)

	assert manager._is_generation_conflict(generation_response)
	assert not manager._is_generation_conflict(leader_response)

	asyncio.run(manager.close())


def test_internal_auth_accepts_current_and_previous_passwords(tmp_path):
	manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())

	manager.authenticate("current-secret")
	manager.authenticate("previous-secret")
	with pytest.raises(HTTPException):
		manager.authenticate("expired-secret")

	asyncio.run(manager.client.aclose())


def test_internal_auth_rejects_the_previous_password_after_expiry(tmp_path):
	configuration = _configuration(tmp_path, 3)
	configuration = replace(configuration, previous_password_valid_until=int(time.time()) - 1)
	manager = ClusterManager(configuration, _MemoryMappings())

	with pytest.raises(HTTPException):
		manager.authenticate("previous-secret")

	asyncio.run(manager.client.aclose())


def test_peer_reads_retry_with_the_previous_password(tmp_path):
	async def run():
		headers = []

		def respond(request):
			headers.append(request.headers["X-Atlas-Cluster-Password"])
			if len(headers) == 1:
				return httpx.Response(401)
			return httpx.Response(200, json={"generation": 4})

		manager = ClusterManager(_configuration(tmp_path, 2), _MemoryMappings())
		await manager.client.aclose()
		manager.client = httpx.AsyncClient(transport=httpx.MockTransport(respond))

		result = await manager._peer_get(manager.configuration.peers[1], "/internal/cluster/status")

		assert result == {"generation": 4}
		assert headers == ["current-secret", "previous-secret"]
		await manager.close()

	asyncio.run(run())


def test_peer_reads_do_not_retry_with_an_expired_password(tmp_path):
	async def run():
		headers = []

		def respond(request):
			headers.append(request.headers["X-Atlas-Cluster-Password"])
			return httpx.Response(401)

		configuration = replace(
			_configuration(tmp_path, 2), previous_password_valid_until=int(time.time()) - 1
		)
		manager = ClusterManager(configuration, _MemoryMappings())
		await manager.client.aclose()
		manager.client = httpx.AsyncClient(transport=httpx.MockTransport(respond))

		with pytest.raises(httpx.HTTPStatusError):
			await manager._peer_get(manager.configuration.peers[1], "/internal/cluster/status")

		assert headers == ["current-secret"]
		await manager.close()

	asyncio.run(run())


def test_an_empty_snapshot_needs_no_openresty_routes(tmp_path):
	manager = ClusterManager(_configuration(tmp_path), _MemoryMappings())

	assert manager.has_snapshot_routes({"sites": 0, "domains": 0})


def test_a_blank_openresty_fails_a_snapshot_with_routes(tmp_path):
	manager = ClusterManager(_configuration(tmp_path), _MemoryMappings())
	manager.snapshot = ClusterSnapshot(sites={"erp": "2001:db8::1"})

	assert not manager.has_snapshot_routes({"sites": 0, "domains": 0})
	assert manager.has_snapshot_routes({"sites": 1, "domains": 0})


def test_lost_domain_routes_fail_the_check(tmp_path):
	manager = ClusterManager(_configuration(tmp_path), _MemoryMappings())
	manager.snapshot = ClusterSnapshot(
		sites={"erp": "2001:db8::1"}, domains={"www.example.com": "2001:db8::2"}
	)

	assert not manager.has_snapshot_routes({"sites": 1, "domains": 0})
	assert manager.has_snapshot_routes({"sites": 1, "domains": 1})


def _follower(tmp_path):
	manager = ClusterManager(_configuration(tmp_path, 3), _MemoryMappings())
	manager.leader_id = "proxy-002"
	manager.is_initialized = True
	manager.is_synchronized = True
	return manager


def _leader_response(status_code, payload):
	request = httpx.Request("POST", "https://proxy-002.example.com/internal/cluster/mutate")
	return httpx.Response(status_code, json=payload, request=request)


def test_a_forwarded_write_keeps_the_leader_conflict_status(tmp_path):
	"""A rejected mutation must not look like an unreachable leader."""

	async def run():
		manager = _follower(tmp_path)
		response = _leader_response(409, {"detail": {"error": "generation mismatch", "generation": 7}})
		manager._peer_post = AsyncMock(
			side_effect=httpx.HTTPStatusError("conflict", request=response.request, response=response)
		)

		with pytest.raises(HTTPException) as raised:
			await manager.mutate(Mutation(kind="sites", action="delete", key="erp"))

		assert raised.value.status_code == 409
		assert raised.value.detail == {"error": "generation mismatch", "generation": 7}
		await manager.close()

	asyncio.run(run())


def test_a_forwarded_write_reports_an_unreachable_leader(tmp_path):
	async def run():
		manager = _follower(tmp_path)
		manager._peer_post = AsyncMock(side_effect=httpx.ReadTimeout("slow"))

		with pytest.raises(HTTPException) as raised:
			await manager.mutate(Mutation(kind="sites", action="delete", key="erp"))

		assert raised.value.status_code == 503
		assert raised.value.detail == {"error": "leader unavailable"}
		await manager.close()

	asyncio.run(run())


def test_a_cluster_password_failure_is_not_the_caller_fault(tmp_path):
	"""The caller authenticated. Only the peer request failed."""

	async def run():
		manager = _follower(tmp_path)
		response = _leader_response(401, {"detail": "unauthorized"})
		manager._peer_post = AsyncMock(
			side_effect=httpx.HTTPStatusError("denied", request=response.request, response=response)
		)

		with pytest.raises(HTTPException) as raised:
			await manager.mutate(Mutation(kind="sites", action="delete", key="erp"))

		assert raised.value.status_code == 502
		await manager.close()

	asyncio.run(run())


def test_the_forward_budget_covers_a_peer_repair(tmp_path):
	"""A repaired peer needs three sequential leader calls. Each one can retry after a password change."""
	assert FORWARDED_WRITE_TIMEOUT_SECONDS > PEER_REPAIR_CALLS * PEER_CALL_ATTEMPTS * PEER_TIMEOUT_SECONDS
