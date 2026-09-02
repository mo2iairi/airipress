#!/usr/bin/env bash
set -Eeuo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
case_root=$(mktemp -d)
trap 'rm -rf -- "$case_root"' EXIT
fake_bin=$case_root/bin
mkdir -p -- "$fake_bin"

cat > "$fake_bin/docker" <<'SH'
#!/usr/bin/env sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = run ]; then
  cat >/dev/null
  printf '%s\n' '$2b$12$LQv3c1yqBWiwLfx5F4cA8uJm0h0jM6c9gZ2t4R7zX5vP1nQ6kL8eS'
fi
SH
chmod +x "$fake_bin/docker"

target=$case_root/airipress
export DOCKER_LOG=$case_root/docker.log
export AIRIPRESS_DOWNLOAD_BASE=file://$repo
export PATH=$fake_bin:$PATH

printf '3111\nalice\na-strong-private-password\na-strong-private-password\n0123456789abcdef0123456789abcdef\n' |
  script -qec "'$repo/deploy/install.sh' '$target'" /dev/null >/dev/null

[[ -f "$target/compose.yaml" && -f "$target/config/config.example.yaml" ]]
grep -q "username: 'alice'" "$target/config/config.yaml"
grep -q '^    password_hash: '\''\$2b\$12\$' "$target/config/config.yaml"
[[ $(cat "$target/config/secrets/airipress_secret") == 0123456789abcdef0123456789abcdef ]]
! grep -q 'a-strong-private-password\|0123456789abcdef0123456789abcdef' "$target/config/config.yaml"
[[ $(stat -c '%a' "$target/config/config.yaml") == 600 ]]
[[ $(stat -c '%a' "$target/config/secrets/airipress_secret") == 600 ]]
[[ $(stat -c '%a' "$target/config/secrets") == 700 ]]
grep -q '^AIRIPRESS_WEB_PORT=3111$' "$target/.env"
[[ ! -e "$target/config/.env" ]]

before_config=$(sha256sum "$target/config/config.yaml")
before_secret=$(sha256sum "$target/config/secrets/airipress_secret")
"$repo/deploy/install.sh" --dry-run "$target" </dev/null >/dev/null
[[ $(sha256sum "$target/config/config.yaml") == "$before_config" ]]
[[ $(sha256sum "$target/config/secrets/airipress_secret") == "$before_secret" ]]

dry_target=$case_root/dry-run
"$repo/deploy/install.sh" --dry-run "$dry_target" </dev/null >/dev/null
[[ ! -e "$dry_target" ]]

grep -q 'pull ghcr.io/mo2iairi/airipress-server:latest' "$DOCKER_LOG"

automatic_target=$case_root/automatic
"$repo/deploy/install.sh" "$automatic_target" </dev/null >/dev/null
grep -q 'compose -f .*/automatic/compose.yaml pull web server' "$DOCKER_LOG"
grep -q 'compose -f .*/automatic/compose.yaml up -d' "$DOCKER_LOG"
echo "install tests passed"
