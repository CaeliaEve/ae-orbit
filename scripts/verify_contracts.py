"""Validate OpenAPI, route coverage, and syntax of deployment YAML."""
import pathlib
import re
import yaml
from jsonschema import Draft202012Validator
from openapi_spec_validator import validate_spec

root = pathlib.Path(__file__).resolve().parents[1]
document = yaml.safe_load((root / "contracts/openapi.yaml").read_text(encoding="utf-8"))
validate_spec(document)
actual = set()
for source in (root / "internal/api").glob("*.go"):
    if not source.name.endswith("_test.go"):
        actual.update((method.lower(), path.replace("{path...}", "{path}"))
                      for method, path in re.findall(r'HandleFunc\("(GET|POST|PUT|DELETE) ([^"]+)"', source.read_text(encoding="utf-8")))
documented = {(method, path) for path, value in document["paths"].items()
              for method in value if method in {"get", "post", "put", "delete", "patch"}}
assert actual == documented, f"Route mismatch: missing={actual-documented}; extra={documented-actual}"

# Exercise the version boundary, not just the shape of the OpenAPI document.
poll = {"session_id": "session", "sequence": "1", "reports": [],
        "read_slots": 16, "read_bytes": 16384, "hints": [{"id": "9007199254740993"}]}
update = {"id": "9007199254740993", "quantity": "9007199254740995",
          "observation": "2", "craftable": None, "assignment_id": "0123456789abcdef" * 2}
poll_ack = {"ack": "1", "commands": [], "reads": [], "poll_millis": 1000, "scan_budget": 0}
cases = [
    ("ProductionJobInput", {"request_key": "manifest-001", "entries": [
        {"entry_key": "source-001", "item_id": "9007199254740993", "quantity": "64"}]}, True),
    ("ProductionJobInput", {"request_key": "manifest-001", "entries": [
        {"entry_key": "source-001", "item_id": "1", "quantity": "64", "cpu_name": "Main CPU", "wait_for_cpu": True}]}, True),
    ("ProductionJobEntry", {"id": "entry", "entry_key": "source-001", "ordinal": 0,
                             "requested_item_id": "1", "item_id": "1", "quantity": "64",
                             "cpu_name": "Main CPU", "wait_for_cpu": True, "state": "queued"}, True),
    ("ProductionJobInput", {"request_key": "manifest-001", "entries": [
        {"entry_key": "source-001", "item_id": "1", "quantity": 64}]}, False),
    ("ProductionJobInput", {"request_key": "manifest-001", "cpu_name": "CPU", "entries": [
        {"entry_key": "source-001", "item_id": "1", "quantity": "64"}]}, False),
    ("ProductionJobControlInput", {"operation_key": "operation-001", "expected_control_version": "2"}, True),
    ("ProductionJobControlInput", {"operation_key": "operation-001", "expected_control_version": 2}, False),
    ("ProductionJobAcceptance", {"receipt": {"job_id": "job", "manifest_digest": "a" * 64, "archived": True}}, True),
    ("ProductionJobAcceptance", {"receipt": {"job_id": "job", "manifest_digest": "a" * 64, "archived": True}, "replayed": True}, False),
    ("ProductionJobChangePage", {"changes": [{"revision": "5", "job_id": "job", "version": "4", "removed": True}],
                                "next_after": "5", "has_more": False, "watermark": "5", "reset": False}, True),
    ("ProductionJobChangePage", {"changes": [], "next_after": 5, "has_more": False, "watermark": "5", "reset": False}, False),
    ("NetworkEvent", {"id": "1", "network_id": "network", "kind": "jobs", "revision": "0",
                      "created_at": "2026-11-11T00:00:00Z"}, True),
    ("PollV2", poll, True),
    ("PollV2", {"session_id": "session", "sequence": "1", "read_slots": 16, "read_bytes": 16384}, True),
    ("Poll", poll, False),
    ("PollV2", {**poll, "read_slots": 17}, False),
    ("PollV2", {**poll, "read_bytes": 16385}, False),
    ("PollV2", {**poll, "hints": [{"id": "1"}] * 17}, False),
    ("PollV2", {**poll, "hints": [{}]}, False),
    ("PollV2", {**poll, "refresh_slots": 1}, False),
    ("UpdateV2", update, True),
    ("UpdateV2", {**update, "recipe_inconclusive": True}, True),
    ("Update", {**update, "recipe_inconclusive": True}, False),
    *[("UpdateV2", {**update, "recipe_inconclusive": value}, False)
      for value in (False, None, 1, "true", {}, [])],
    ("UpdateV2", {**update, "recipe_inconclusive": True, "craftable": True}, False),
    ("UpdateV2", {**update, "recipe_inconclusive": True, "craftable": False}, False),
    ("UpdateV2", {key: value for key, value in {**update, "recipe_inconclusive": True}.items()
                  if key != "assignment_id"}, False),
    ("UpdateV2", {**{key: value for key, value in update.items() if key != "id"},
                  "item": {"kind": "item", "name": "test:item", "damage": 0, "label": "Item", "exact": True},
                  "recipe_inconclusive": True}, False),
    ("Update", update, False),
    ("UpdateV2", {**update, "quantity": 9007199254740995}, False),
    ("UpdateV2", {**update, "assignment_id": "A" * 32}, False),
    ("UpdateV2", {**update, "verification": "a" * 32}, False),
    ("UpdateV2", {key: value for key, value in update.items() if key != "craftable"}, False),
    ("PollAckV2", poll_ack, True),
    ("PollAckV2", {key: value for key, value in poll_ack.items() if key != "reads"}, False),
    ("PollAckV2", {**poll_ack, "verify": []}, False),
    ("PollAckV2", {**poll_ack, "refresh": []}, False),
    ("Capabilities", {"cpu_selection": True}, True),
    ("OrderInput", {"item_id": "1", "quantity": "64", "idempotency_key": "order-001",
                    "cpu_name": "Main CPU", "wait_for_cpu": True}, True),
    ("Command", {"order_id": "order", "attempt_id": "attempt", "item_id": "1",
                  "item": {"kind": "item", "name": "minecraft:stone", "damage": 0, "label": "Stone", "exact": True},
                  "quantity": "64", "current_quantity": "0", "cpu_name": "Main CPU", "wait_for_cpu": True}, True),
]
detail={'assignment_id':'a'*32,'cpu_id':'1:1','kind':'active','ttl_millis':8000}
result={'assignment_id':'a'*32,'cpu_id':'1:1','kind':'active','total':0,'truncated':False}
cases += [('CPUDetailAssignment',detail,True),('CPUDetailAssignment',{**detail,'ttl_millis':8001},False),
          ('CPUDetailResult',result,True),('CPUDetailResult',{**result,'kind':'network'},False),
          ('CPUDetailResult',{**result,'items':[{'row_id':'row','kind':'item','name':'test:item','label':'Item','quantity':64}]},False),
          ('PollV2',{'session_id':'session','sequence':'1','cpu_detail':result},True),
          ('Poll',{'session_id':'session','sequence':'1','cpu_detail':result},False),
          ('PollAckV2',{**poll_ack,'cpu_detail':detail},True)]
flow={"input_eut":"100","output_eut":"80","loss_eut":"5","avg_input_eut":"101","avg_output_eut":"79","window_seconds":5,"source":"lsc_sensor_v1"}
monitor_sample={"object_id":"a"*32,"revision":1,"installation_revision":1,"sample_millis":1000,"stored_eu":"1000","capacity_eu":"10000"}
cases += [("MonitorSample",monitor_sample,True),("MonitorSample",{**monitor_sample,"flow":flow},True),
 ("MonitorSample",{**monitor_sample,"flow_error":"unsupported"},True),
 ("MonitorSample",{**monitor_sample,"flow":{**flow,"input_eut":100}},False),
 ("MonitorSample",{**monitor_sample,"flow":flow,"flow_error":"unsupported"},False),
 ("MonitorSample",{"object_id":"a"*32,"revision":1,"installation_revision":1,"sample_millis":1000,"error":"controller_mismatch"},True)]
cases += [('MonitorHello',{'boot_id':'a'*32,'modules':['gtnh.lsc'],'components':[],'min_interval_seconds':1},True),
          ('MonitorHello',{'boot_id':'a'*32,'modules':['gtnh.lsc'],'components':[],'min_interval_seconds':0},False),
          ('MonitorSettings',{'interval_seconds':1,'history_interval_seconds':10,'low_percent':20,'recover_percent':25,'low_duration_seconds':30,'offline_seconds':60,'falling_minutes':15,'raw_retention_days':30,'alerts':True},True)]
for name, value, expected in cases:
    schema = {"$ref": f"#/components/schemas/{name}", "components": document["components"]}
    valid = Draft202012Validator(schema).is_valid(value)
    assert valid == expected, f"{name}: expected valid={expected}: {value}"
for name in ("compose.yaml", "compose.https.yaml", "compose.catalog.yaml"):
    config = yaml.safe_load((root / "deploy" / name).read_text(encoding="utf-8"))
    assert isinstance(config["services"], dict)
print(f"PASS OpenAPI schema and {len(actual)} HTTP operations; deployment YAML parses.")
print(f"PASS {len(cases)} v1/v2/job wire boundary examples.")
print("Container builds and docker compose runtime validation require Docker.")
