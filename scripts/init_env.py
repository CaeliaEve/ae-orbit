"""Create local Compose secrets without displaying them or replacing an existing file."""
import os
import pathlib
import secrets

root = pathlib.Path(__file__).resolve().parents[1]
target = root / ".env"
content = (root / ".env.example").read_text(encoding="utf-8")
content = content.replace("POSTGRES_PASSWORD=\n", "POSTGRES_PASSWORD=" + secrets.token_hex(32) + "\n")
content = content.replace("AE_ADMIN_TOKEN=\n", "AE_ADMIN_TOKEN=" + secrets.token_hex(32) + "\n")
try:
    descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    raise SystemExit(".env already exists; its values have been preserved.")
with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as stream:
    stream.write(content)
print("Created .env with fresh local credentials. Edit AE_DOMAIN for HTTPS deployment.")
