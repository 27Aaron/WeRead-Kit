#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

hashes_file="nix/hashes.json"
repo="27Aaron/WeRead-Kit"

original_hashes=$(cat "$hashes_file")
restore_on_error() { printf '%s\n' "$original_hashes" >"$hashes_file"; }
trap restore_on_error ERR INT TERM

auth=()
if [[ -n ${GITHUB_TOKEN:-} ]]; then
  auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
fi

if [[ $# -ge 1 && -n $1 ]]; then
  latest_tag="v${1#v}"
else
  latest_tag=$(curl -sf -H "Accept: application/vnd.github+json" "${auth[@]}" \
    "https://api.github.com/repos/${repo}/releases/latest" | jq -r .tag_name)
  [[ -n $latest_tag && $latest_tag != "null" ]] || {
    echo "无法获取最新 Release" >&2
    exit 1
  }
fi
version=${latest_tag#v}

current=$(jq -r .version "$hashes_file")
if [[ $version == "$current" ]]; then
  echo "当前已记录版本 ${current}，无需更新"
  exit 0
fi

echo "更新包版本：${current} → ${version}"

# srcHash：预取 GitHub 源码，获取 SRI 格式的 NAR 哈希。
src_hash=$(nix flake prefetch --json "github:${repo}/${latest_tag}" | jq -r .hash)
[[ -n $src_hash ]] || {
  echo "预取 ${latest_tag} 源码失败" >&2
  exit 1
}

write_hashes() { # $1=version $2=srcHash $3=vendorHash
  jq -n --arg v "$1" --arg s "$2" --arg m "$3" \
    '{version: $v, srcHash: $s, vendorHash: $m}' >"$hashes_file"
}

vendor_old=$(jq -r .vendorHash "$hashes_file")
write_hashes "$version" "$src_hash" "$vendor_old"

# vendorHash：先尝试旧哈希；发生哈希不匹配时，提取 got 值并重试。
# 无匹配时 grep 返回 1，使用 || true 保留后续错误诊断。
for attempt in 1 2 3; do
  if build_out=$(nix build .#weread-kit.goModules --no-link 2>&1); then
    break
  fi
  got=$(printf '%s\n' "$build_out" |
    grep -oE 'got: +sha256-[A-Za-z0-9+/=]+' | tail -1 | awk '{print $2}') || true
  if [[ -z $got ]]; then
    printf '%s\n' "$build_out" >&2
    echo "无法从构建错误中解析 vendorHash" >&2
    exit 1
  fi
  jq --arg m "$got" '.vendorHash = $m' "$hashes_file" >"$hashes_file.tmp" \
    && mv "$hashes_file.tmp" "$hashes_file"
  if [[ $attempt == 3 ]]; then
    echo "vendorHash 多次回填后构建仍失败" >&2
    exit 1
  fi
done

nix build .#weread-kit --no-link
echo "已更新 hashes.json 至版本 ${version}，包构建通过"
