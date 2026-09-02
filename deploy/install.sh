#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "Usage: install.sh [--dry-run] [--version VERSION] [PROJECT_DIR]" >&2
  exit 2
}

dry_run=0
release_version=${AIRIPRESS_VERSION:-latest}
version_requested=0
project=
while (($#)); do
  case "$1" in
    --dry-run) dry_run=1; shift ;;
    --version) (($# > 1)) || usage; release_version=$2; version_requested=1; shift 2 ;;
    -*) usage ;;
    *) [[ -z "$project" ]] || usage; project=$1; shift ;;
  esac
done
[[ "$release_version" =~ ^[A-Za-z0-9._-]+$ ]] || { echo "Invalid version" >&2; exit 1; }

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || true)
repo_root=$(cd -- "$script_dir/.." 2>/dev/null && pwd || true)
if [[ -z "$project" ]]; then
  if [[ -f "$repo_root/compose.yaml" && -f "$repo_root/config/config.example.yaml" ]]; then
    project=$repo_root
  else
    project=$PWD/airipress
  fi
fi
case "${project%/}" in /|/home|/root|/tmp) echo "Refusing dangerous project path: $project" >&2; exit 1 ;; esac

if ((dry_run)); then
  echo "dry-run: would install airipress $release_version into $project"
  echo "dry-run: would download missing compose.yaml and config/config.example.yaml"
  echo "dry-run: would create config/config.yaml, config/secrets/airipress_secret and .env only when absent"
  echo "dry-run: would pull and start the web and server images"
  exit 0
fi

mkdir -p -- "$project"
project=$(cd -- "$project" && pwd)

compose=$project/compose.yaml
example=$project/config/config.example.yaml
config=$project/config/config.yaml
secret_dir=$project/config/secrets
secret_file=$secret_dir/airipress_secret
deployment_env=$project/.env
if [[ -n ${AIRIPRESS_DOWNLOAD_REF:-} ]]; then
  download_ref=$AIRIPRESS_DOWNLOAD_REF
elif [[ "$release_version" == latest ]]; then
  download_ref=main
else
  download_ref=$release_version
  [[ "$download_ref" =~ ^[0-9] ]] && download_ref="v$download_ref"
fi
download_base=${AIRIPRESS_DOWNLOAD_BASE:-https://raw.githubusercontent.com/mo2iairi/airipress/$download_ref}

download_if_missing() {
  local relative=$1 target=$2 temporary
  [[ ! -e "$target" ]] || return 0
  command -v curl >/dev/null 2>&1 || { echo "curl is required to download $relative" >&2; exit 1; }
  mkdir -p -- "$(dirname -- "$target")"
  temporary=$target.download.$$
  trap 'rm -f -- "$compose.download.$$" "$example.download.$$"' EXIT
  curl -fsSL "$download_base/$relative" -o "$temporary"
  [[ -s "$temporary" ]] || { echo "Downloaded $relative is empty" >&2; exit 1; }
  mv -- "$temporary" "$target"
}

download_if_missing compose.yaml "$compose"
download_if_missing config/config.example.yaml "$example"
mkdir -p -- "$secret_dir" "$project/data" "$project/imports"
umask 077
chmod 700 "$secret_dir"

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

generated_username=0
generated_password=0
web_port=${AIRIPRESS_WEB_PORT:-3000}
[[ "$web_port" =~ ^[0-9]+$ ]] && ((web_port >= 1 && web_port <= 65535)) || { echo "AIRIPRESS_WEB_PORT must be between 1 and 65535" >&2; exit 1; }
interactive=0
if { exec 3<>/dev/tty; } 2>/dev/null && [[ -t 3 ]]; then interactive=1; fi
if [[ ! -e "$config" ]]; then
  if ((interactive)); then
    read -r -u 3 -p "Web port [$web_port]: " selected_port
    web_port=${selected_port:-$web_port}
    [[ "$web_port" =~ ^[0-9]+$ ]] && ((web_port >= 1 && web_port <= 65535)) || { echo "Web port must be between 1 and 65535" >&2; exit 1; }
    read -r -u 3 -p "Admin username (empty generates one): " admin_user
    if [[ -z "$admin_user" ]]; then admin_user="admin_$(random_secret)"; admin_user=${admin_user:0:22}; generated_username=1; fi
    read -r -s -u 3 -p "Admin password (empty generates one): " admin_pass; echo
    if [[ -z "$admin_pass" ]]; then
      admin_pass=$(random_secret); generated_password=1
    else
      read -r -s -u 3 -p "Confirm admin password: " admin_confirm; echo
      [[ "$admin_pass" == "$admin_confirm" ]] || { echo "Passwords do not match" >&2; exit 1; }
    fi
    read -r -s -u 3 -p "SECRET (empty generates one): " secret; echo
    secret=${secret:-$(random_secret)}
  else
    admin_user="admin_$(random_secret)"; admin_user=${admin_user:0:22}; generated_username=1
    admin_pass=$(random_secret); generated_password=1; secret=$(random_secret)
  fi
  [[ "$admin_user" =~ ^[A-Za-z0-9._-]{1,64}$ ]] || { echo "Invalid administrator username" >&2; exit 1; }
  [[ ${#admin_pass} -ge 12 ]] || { echo "Administrator password must contain at least 12 characters" >&2; exit 1; }
  [[ $(printf '%s' "$admin_pass" | wc -c) -le 72 ]] || { echo "Administrator password must not exceed 72 bytes" >&2; exit 1; }
  [[ ${#secret} -ge 32 ]] || { echo "SECRET must contain at least 32 characters" >&2; exit 1; }
  command -v docker >/dev/null 2>&1 || { echo "Docker is required" >&2; exit 1; }
  command -v python3 >/dev/null 2>&1 || { echo "python3 is required to create config/config.yaml" >&2; exit 1; }
  server_image="ghcr.io/${AIRIPRESS_IMAGE_OWNER:-mo2iairi}/airipress-server:$release_version"
  docker pull "$server_image" >/dev/null || { echo "Unable to pull $server_image. Ensure the GHCR package is public or run docker login ghcr.io first." >&2; exit 1; }
  admin_hash=$(printf '%s\n' "$admin_pass" | docker run --rm -i "$server_image" hash-password)
  [[ "$admin_hash" =~ ^\$2[aby]\$12\$[./A-Za-z0-9]{53}$ ]] || { echo "hash-password returned an invalid bcrypt cost-12 hash" >&2; exit 1; }
  temporary_config=$config.new.$$
  python3 - "$temporary_config" "$web_port" "$admin_user" "$admin_hash" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
quote = lambda value: "'" + value.replace("'", "''") + "'"
path.write_text("\n".join([
    "version: 1", "auth:", "  admin:",
    "    username: " + quote(sys.argv[3]),
    "    password_hash: " + quote(sys.argv[4]),
    "  session:", "    ttl: 24h", "    cookie_secure: false",
    "server:", "  allowed_origins:",
    "    - " + quote("http://localhost:" + sys.argv[2]),
    "    - " + quote("http://127.0.0.1:" + sys.argv[2]), "",
]), encoding="utf-8")
PY
  [[ ! -e "$config" ]] || { rm -f -- "$temporary_config"; echo "config/config.yaml appeared during installation; refusing to overwrite it" >&2; exit 1; }
  mv -- "$temporary_config" "$config"
  printf '%s\n' "$secret" > "$secret_file"
  chmod 600 "$config" "$secret_file"

else
  [[ -f "$config" ]] || { echo "config/config.yaml is not a regular file" >&2; exit 1; }
  [[ -f "$secret_file" ]] || { echo "Missing $secret_file; restore the original SECRET before starting" >&2; exit 1; }
  chmod 600 "$config" "$secret_file"
fi

if [[ ! -e "$deployment_env" ]]; then
  temporary_env=$deployment_env.new.$$
  cat > "$temporary_env" <<EOF
# Docker Compose deployment settings. Authentication and OAuth remain in config/config.yaml.
AIRIPRESS_WEB_PORT=$web_port
AIRIPRESS_BIND_ADDRESS=${AIRIPRESS_BIND_ADDRESS:-127.0.0.1}
AIRIPRESS_IMAGE_OWNER=${AIRIPRESS_IMAGE_OWNER:-mo2iairi}
AIRIPRESS_VERSION=$release_version
AIRIPRESS_DATA_DIR=./data
AIRIPRESS_IMPORT_DIR=./imports
EOF
  mv "$temporary_env" "$deployment_env"
  chmod 600 "$deployment_env"
elif ((version_requested)); then
  temporary_env=$deployment_env.new.$$
  awk -v value="AIRIPRESS_VERSION=$release_version" 'BEGIN { found=0 } /^AIRIPRESS_VERSION=/ { print value; found=1; next } { print } END { if (!found) print value }' "$deployment_env" > "$temporary_env"
  mv "$temporary_env" "$deployment_env"
  chmod 600 "$deployment_env"
fi

python3 - "$config" <<'PY'
import pathlib, re, sys
text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
if re.search(r"(?mi)^\s*(?:password|admin_password|secret)\s*:", text):
    raise SystemExit("plaintext password/secret fields are forbidden in config.yaml")
version = re.search(r"(?m)^version:\s*['\"]?([^'\"\s]+)", text)
username = re.search(r"(?m)^\s{4}username:\s*['\"]?([^'\"\r\n]+)", text)
password_hash = re.search(r"(?m)^\s{4}password_hash:\s*['\"]?(\$2[aby]\$12\$[./A-Za-z0-9]{53})", text)
if not version or version.group(1) != "1" or not username or not password_hash:
    raise SystemExit("config.yaml must use version 1 and contain admin username plus a cost-12 bcrypt password_hash")
if not re.fullmatch(r"[A-Za-z0-9._-]{1,64}", username.group(1)):
    raise SystemExit("invalid administrator username in config.yaml")
PY

if ((generated_username)); then echo "Generated administrator username: $admin_user"; fi
if ((generated_password)); then echo "Generated administrator password (shown once): $admin_pass"; fi
echo "SECRET is stored at $secret_file; keep this file backed up and private."

docker compose -f "$compose" pull web server
docker compose -f "$compose" up -d
echo "airipress is running at http://localhost:3000"
