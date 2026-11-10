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
]
for name, value, expected in cases:
    schema = {"$ref": f"#/components/schemas/{name}", "components": document["components"]}
    valid = Draft202012Validator(schema).is_valid(value)
    assert valid == expected, f"{name}: expected valid={expected}: {value}"
for name in ("compose.yaml", "compose.https.yaml"):
    config = yaml.safe_load((root / "deploy" / name).read_text(encoding="utf-8"))
    assert isinstance(config["services"], dict)
print(f"PASS OpenAPI schema and {len(actual)} HTTP operations; deployment YAML parses.")
print(f"PASS {len(cases)} v1/v2 wire boundary examples.")
print("Container builds and docker compose runtime validation require Docker.")
