"""Validate OpenAPI, route coverage, and syntax of deployment YAML."""
import pathlib
import re
import yaml
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
for name in ("compose.yaml", "compose.https.yaml"):
    config = yaml.safe_load((root / "deploy" / name).read_text(encoding="utf-8"))
    assert isinstance(config["services"], dict)
print(f"PASS OpenAPI schema and {len(actual)} HTTP operations; deployment YAML parses.")
print("Container builds and docker compose runtime validation require Docker.")
