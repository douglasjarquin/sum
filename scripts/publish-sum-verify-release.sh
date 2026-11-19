#!/usr/bin/env bash
# Finish the sum-verify release that a merged release-please PR approved.
#
#   publish-sum-verify-release.sh resolve
#   publish-sum-verify-release.sh publish <tag> <version> <dist dir> <toolchain report>
#
# resolve runs in a main checkout with full history and tags. It reads the
# released version from .release-please-manifest.json, which only a merged
# release PR changes, and prints action=none, action=done, or action=publish
# with tag, version, and sha (also to $GITHUB_OUTPUT when set):
#   none     no version yet, or release-please has not created the draft yet
#   done     the release is published with every asset (a validated no-op)
#   publish  a draft exists whose tag is the release PR's merge commit on main
# It does not depend on whether this run created the release, so a full rerun
# or any later push to main finishes a draft an earlier run left behind.
# It fails, before anything is built, if the tag is not the merge commit of
# exactly one merged release-please PR for that version, or is not on main.
#
# publish attaches the binaries and checksums to the draft, appends the pin
# command and the build's toolchain report to the release-please notes, then
# publishes. Uploads
# replace partial assets from an earlier failed attempt; an already published
# complete release is a no-op.
#
# MAIN_REF (default origin/main) names main for the ancestry check.
set -euo pipefail

component="sum-verify"
release_branch="release-please--branches--main--components--$component"
assets=(sum-verify-darwin-amd64 sum-verify-darwin-arm64 sum-verify-linux-amd64 sum-verify-linux-arm64 checksums.txt)
main_ref="${MAIN_REF:-origin/main}"

fail() { echo "$*" >&2; exit 1; }

emit() {
  local line
  for line in "$@"; do
    echo "$line"
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then echo "$line" >> "$GITHUB_OUTPUT"; fi
  done
}

manifest_version() {
  git show "$1:.release-please-manifest.json" | python3 -c 'import json, sys; print(json.load(sys.stdin).get("tools/verify", ""))'
}

# Prints the release's draft state (true/false), or nothing if it does not exist.
release_state() {
  local out
  if out="$(gh release view "$1" --json isDraft --jq .isDraft 2>&1)"; then
    echo "$out"
  elif [[ "$out" == *"release not found"* ]]; then
    :
  else
    fail "cannot read release $1: $out"
  fi
}

require_complete() {
  local tag="$1" names name
  names="$(gh release view "$tag" --json assets --jq '.assets[].name')"
  for name in "${assets[@]}"; do
    grep -qxF "$name" <<< "$names" || fail "published release $tag is missing $name"
  done
}

resolve() {
  local version tag state merges merge tagged
  version="$(manifest_version HEAD)"
  if [[ -z "$version" ]]; then
    emit "action=none"
    return
  fi
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "manifest version $version is not X.Y.Z"
  tag="$component-v$version"

  state="$(release_state "$tag")"
  if [[ -z "$state" ]]; then
    echo "no release $tag yet; release-please creates it after the release PR merges" >&2
    emit "action=none"
    return
  fi

  # The approval is the merged release PR: exactly one, for exactly this version.
  merges="$(gh pr list --state merged --base main --head "$release_branch" --limit 100 \
    --json title,mergeCommit --jq '.[] | [.title, .mergeCommit.oid] | @tsv' \
    | awk -F '\t' -v want="release $component $version" '{ n = length($1) - length(want); if (n >= 0 && substr($1, n + 1) == want) print $2 }')"
  [[ -n "$merges" ]] || fail "no merged release PR approves $tag"
  [[ "$(wc -l <<< "$merges")" -eq 1 ]] || fail "more than one merged release PR names $tag"
  merge="$merges"

  [[ "$(manifest_version "$merge")" == "$version" ]] || fail "release PR merge $merge does not set version $version"
  git merge-base --is-ancestor "$merge" "$main_ref" || fail "release PR merge $merge is not on $main_ref"
  tagged="$(git rev-parse -q --verify "refs/tags/$tag^{commit}")" || fail "tag $tag does not exist"
  [[ "$tagged" == "$merge" ]] || fail "tag $tag points at $tagged, but its release PR merged as $merge"

  if [[ "$state" == false ]]; then
    require_complete "$tag"
    echo "release $tag is already published and complete" >&2
    emit "action=done" "tag=$tag" "version=$version" "sha=$merge"
    return
  fi
  emit "action=publish" "tag=$tag" "version=$version" "sha=$merge"
}

publish() {
  local usage="usage: publish <tag> <version> <dist dir> <toolchain report>"
  local tag="${1:?$usage}" version="${2:?$usage}" dist="${3:?$usage}" toolchain="${4:?$usage}"
  local state name notes
  [[ "$tag" == "$component-v$version" && "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "tag $tag does not match version $version"
  for name in "${assets[@]}"; do
    [[ -f "$dist/$name" ]] || fail "missing build output $dist/$name"
  done
  [[ -s "$toolchain" ]] || fail "missing toolchain report $toolchain"

  state="$(release_state "$tag")"
  [[ -n "$state" ]] || fail "release $tag does not exist"
  if [[ "$state" == false ]]; then
    require_complete "$tag"
    echo "release $tag is already published and complete" >&2
    return
  fi

  for name in "${assets[@]}"; do
    gh release upload "$tag" "$dist/$name" --clobber
  done
  notes="$(mktemp)"
  trap 'rm -f "$notes"' RETURN
  {
    gh release view "$tag" --json body --jq .body
    echo
    echo "Pin with: .agents/skills/verify/scripts/sum-verify pin $version"
    echo
    echo "Built with:"
    echo
    echo '```'
    cat "$toolchain"
    echo '```'
  } > "$notes"
  gh release edit "$tag" --notes-file "$notes" --draft=false
}

case "${1:-}" in
  resolve) resolve ;;
  publish) shift; publish "$@" ;;
  *) fail "usage: $0 resolve | publish <tag> <version> <dist dir> <toolchain report>" ;;
esac
