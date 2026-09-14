#!/usr/bin/env bash
# Mechanical context for the dev -> main release PR body (release-pr-sync.yml).
# Deterministic; the agent step reasons over this file. Ported from the stoa /
# specstory-sync pattern and tuned to this repo's layout.
#
# Usage: build-release-pr-context.sh [base-ref] [head-ref] [output-path]
# Needs: git with both refs fetched; gh + GH_TOKEN for the factory-merge section
# (degrades to commit subjects if gh is unavailable).

set -euo pipefail

BASE_REF="${1:-origin/main}"
HEAD_REF="${2:-origin/dev}"
OUTPUT_PATH="${3:--}"
REPO="${GITHUB_REPOSITORY:-specstoryai/getspecstory}"

tmp_file="$(mktemp)"
trap 'rm -f "$tmp_file"' EXIT

commit_count="$(git rev-list --count --no-merges "${BASE_REF}..${HEAD_REF}")"
shortstat="$(git diff --shortstat "${BASE_REF}..${HEAD_REF}" || true)"

{
  echo "# Release PR Context"
  echo
  echo "- Generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo "- Compare range: \`${BASE_REF}..${HEAD_REF}\`"
  echo "- Non-merge commit count: ${commit_count}"
  echo "- Diff summary: ${shortstat:-No file changes}"
  echo

  echo "## Paths Changed (top two levels)"
  echo
  if git diff --name-only "${BASE_REF}..${HEAD_REF}" | grep -q .; then
    git diff --name-only "${BASE_REF}..${HEAD_REF}" \
      | awk -F/ 'NF>1 {print $1"/"$2} NF==1 {print $1}' \
      | sort | uniq -c | sort -rn \
      | sed 's/^ *//; s/ /x /' | sed 's/^/- /'
  else
    echo "- None"
  fi
  echo

  echo "## Changelog Entries Added in This Range (specstory-cli/changelog.md)"
  echo
  echo "The CLI's changelog is authored per release; lines added between the refs are the"
  echo "maintainers' own description of what shipped. Treat as a primary source when present."
  echo
  if git diff "${BASE_REF}..${HEAD_REF}" -- specstory-cli/changelog.md | grep -q '^+[^+]'; then
    echo '```'
    git diff "${BASE_REF}..${HEAD_REF}" -- specstory-cli/changelog.md | grep '^+[^+]' | sed 's/^+//'
    echo '```'
  else
    echo "- None (no changelog edits in this range — synthesize from commits and diffs)"
  fi
  echo

  echo "## Changed Design / Implementation Docs (specstory-cli/docs)"
  echo
  doc_count=0
  while IFS= read -r path; do
    doc_count=1
    echo "- \`${path}\`"
  done < <(git diff --name-only "${BASE_REF}..${HEAD_REF}" -- 'specstory-cli/docs/*.md')
  if [[ "${doc_count}" -eq 0 ]]; then
    echo "- None"
  fi
  echo

  echo "## Dependency Updates Merged by the Provider Factory"
  echo
  echo "Dependabot PRs merged into dev by the factory's DEPENDENCY-UPDATE workflow. Each carries"
  echo "an assessment comment (verdict + reasoning) on the PR — link them; do not re-derive."
  echo
  dep_count=0
  while IFS= read -r line; do
    pr="$(sed -nE 's/.*Merge pull request #([0-9]+) from [^ ]*dependabot.*/\1/p' <<<"${line}")"
    [[ -n "${pr}" ]] || continue
    dep_count=1
    title="$(sed -E 's/^[0-9a-f]+ //' <<<"${line}")"
    if command -v gh >/dev/null 2>&1 && [[ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]]; then
      pr_title="$(gh pr view "${pr}" --repo "${REPO}" --json title --jq .title 2>/dev/null || echo "${title}")"
      assess="$(gh api "repos/${REPO}/issues/${pr}/comments" --paginate --jq '.[] | select(.body | test("factory:dependency-update")) | "\(.html_url) \(.body | capture("verdict=(?<v>[a-z]+)").v)"' 2>/dev/null | tail -1)"
      if [[ -n "${assess}" ]]; then
        echo "- #${pr} ${pr_title} — verdict: ${assess##* } — [assessment](${assess%% *})"
      else
        echo "- #${pr} ${pr_title} — (no factory assessment found)"
      fi
    else
      echo "- #${pr} ${title}"
    fi
  done < <(git log --merges --format='%h %s' "${BASE_REF}..${HEAD_REF}")
  if [[ "${dep_count}" -eq 0 ]]; then
    echo "- None"
  fi
  echo

  echo "## Non-Merge Commits"
  echo
  git log --no-merges --format='- %h %s (%an)' "${BASE_REF}..${HEAD_REF}"
  echo

  echo "## Files Changed"
  echo
  echo '```'
  git diff --stat=120 "${BASE_REF}..${HEAD_REF}" | tail -n 200
  echo '```'
} > "${tmp_file}"

if [[ "${OUTPUT_PATH}" == "-" ]]; then
  cat "${tmp_file}"
else
  mkdir -p "$(dirname "${OUTPUT_PATH}")"
  cp "${tmp_file}" "${OUTPUT_PATH}"
  echo "Release PR context written to ${OUTPUT_PATH} ($(wc -l < "${OUTPUT_PATH}") lines)"
fi
