#!/usr/bin/env bash
set -euo pipefail

## update-release-version.sh
## Deterministically updates the CodeQL CLI version across all version-bearing files.
##
## Version-bearing files:
##   .codeql-version                    (vX.Y.Z, base version only)
##   ql/*/lib/codeql-pack.yml           (X.Y.Z[-suffix])
##   ql/*/*/{src,test}/codeql-pack.yml  (X.Y.Z[-suffix])
##   internal/runner/printast.go        (X.Y.Z, ql-mcp tool pack ABOM runs PrintAST from)
##   internal/harness/copilot.go        (X.Y.Z, ql-mcp release the harness starts)
##
## Only ABOM's own pack versions take a pre-release suffix; the CLI pin and the ql-mcp pins name
## published releases. Upstream pack pins are moved by upgrade-packs.sh, not here.
##
## Usage:
##   ./scripts/update-release-version.sh <new-version>
##   ./scripts/update-release-version.sh --check [<expected-version>]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

PRINTAST_GO="internal/runner/printast.go"
COPILOT_GO="internal/harness/copilot.go"

usage() {
	cat <<EOF
Usage: $0 <new-version>
       $0 --check [<expected-version>]

Deterministically updates the CodeQL CLI version across all version-bearing files.
The base version (X.Y.Z, without prerelease suffixes) must correspond to an
actual CodeQL CLI release.

ARGUMENTS:
    <new-version>          The new version to set (e.g., 2.27.2 or 2.27.2-next.1).
                           The 'v' prefix is optional and will be normalized.

OPTIONS:
    --check [<version>]    Check version consistency across all files.
                           If <version> is provided, also validates that all files
                           match the expected version.
    --dry-run              Show what would be changed without modifying files.
    --skip-cli-validation  Skip CodeQL CLI version validation (not recommended).
    -h, --help             Show this help message.

EXAMPLES:
    $0 2.27.2              Update all files to version 2.27.2
    $0 --check             Verify all version-bearing files are consistent
    $0 --check 2.27.2      Verify all files contain version 2.27.2
    $0 --dry-run 2.27.2    Preview changes without writing files
EOF
}

pack_manifests() {
	local manifest
	for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
		[[ -e "${manifest}" ]] && echo "${manifest}"
	done
	return 0
}

read_printast_version() {
	sed -n 's/^const ToolsPack = toolsPackName + "@\([^"]*\)".*/\1/p' "${REPO_ROOT}/${PRINTAST_GO}" | head -1
}

read_copilot_version() {
	sed -n 's/.*codeql-development-mcp-server@\([^" ]*\).*/\1/p' "${REPO_ROOT}/${COPILOT_GO}" | head -1
}

## Emits "file|version|kind" where kind is "base" for files that only ever carry X.Y.Z.
collect_versions() {
	local raw manifest
	raw=$(tr -d '[:space:]' <"${REPO_ROOT}/.codeql-version")
	echo ".codeql-version|${raw#v}|base"

	while IFS= read -r manifest; do
		echo "${manifest}|$(awk '/^version:/ {print $2; exit}' "${REPO_ROOT}/${manifest}")|full"
	done < <(cd "${REPO_ROOT}" && pack_manifests)

	echo "${PRINTAST_GO}|$(read_printast_version)|base"
	echo "${COPILOT_GO}|$(read_copilot_version)|base"
}

check_versions() {
	local expected_version="${1:-}"
	local all_consistent=true
	local first_version=""
	local file_count=0
	local file version kind want

	echo "=== Version Consistency Check ==="
	echo ""

	while IFS='|' read -r file version kind; do
		file_count=$((file_count + 1))
		if [[ -z "${first_version}" ]]; then
			first_version="${version}"
		fi

		if [[ -n "${expected_version}" ]]; then
			want="${expected_version}"
		else
			want="${first_version}"
		fi
		if [[ "${kind}" == "base" ]]; then
			want="${want%%-*}"
		fi

		if [[ "${version}" == "${want}" ]]; then
			echo "  ✅ ${file}: ${version}"
		elif [[ -z "${expected_version}" && "${kind}" == "full" && "${version%%-*}" == "${want}" ]]; then
			## Without an expected version, a pack may carry a pre-release of the pinned CLI.
			echo "  ✅ ${file}: ${version}"
		else
			echo "  ❌ ${file}: ${version:-<missing>} (expected ${want})"
			all_consistent=false
		fi
	done < <(collect_versions)

	echo ""
	echo "Checked ${file_count} version-bearing files."

	if [[ "${all_consistent}" == true ]]; then
		echo "✅ All files are consistent at version: ${expected_version:-${first_version}}"
		return 0
	fi
	echo "❌ Version inconsistency detected!"
	return 1
}

validate_version() {
	local version="$1"
	if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]; then
		echo "ERROR: Invalid version format '${version}'" >&2
		echo "Expected format: X.Y.Z or X.Y.Z-suffix (e.g., 2.27.2 or 2.27.2-next.1)" >&2
		return 1
	fi
}

## Checks the installed CLI first, then falls back to .codeql-version.
validate_cli_version() {
	local base_version="$1"

	echo "=== CodeQL CLI Version Validation ==="
	echo "  Required base version: ${base_version}"

	if command -v codeql >/dev/null 2>&1; then
		local installed_version
		installed_version=$(codeql version --format=terse || echo "unknown")
		echo "  Installed CLI version: ${installed_version}"
		if [[ "${installed_version}" == "${base_version}" ]]; then
			echo "  ✅ Base version ${base_version} matches installed CodeQL CLI"
			return 0
		fi
		echo "  ❌ Base version ${base_version} does not match installed CodeQL CLI (${installed_version})" >&2
		## gh-codeql runs what .codeql-version names, so `gh codeql set-version` cannot fix this.
		echo "    - From the repository root, pin and install it, then re-run: gh codeql set-local-version v${base_version}" >&2
		echo "    - Skip validation with --skip-cli-validation (not recommended)" >&2
		return 1
	fi

	local file_version
	file_version=$(tr -d '[:space:]' <"${REPO_ROOT}/.codeql-version")
	file_version="${file_version#v}"
	echo "  .codeql-version file: ${file_version}"
	if [[ "${file_version}" == "${base_version}" ]]; then
		echo "  ✅ Base version ${base_version} matches .codeql-version"
		return 0
	fi
	echo "  ❌ Base version ${base_version} does not match .codeql-version (${file_version})" >&2
	echo "    - Skip validation with --skip-cli-validation (not recommended)" >&2
	return 1
}

## Replaces exactly one match of a sed pattern, failing if the pattern matches zero or many lines.
replace_once() {
	local file="$1" grep_pattern="$2" sed_expr="$3"
	local matches
	matches=$(grep -c -- "${grep_pattern}" "${file}" || true)
	if [[ "${matches}" -ne 1 ]]; then
		echo "ERROR: expected exactly one '${grep_pattern}' in ${file}, found ${matches}" >&2
		exit 1
	fi
	sed -i.bak "${sed_expr}" "${file}"
	rm -f "${file}.bak"
}

update_versions() {
	local new_version="$1"
	local dry_run="${2:-false}"
	local base_version="${new_version%%-*}"
	local updated_count=0
	local old manifest

	echo "=== Updating Release Version to ${new_version} ==="
	echo ""

	old=$(tr -d '[:space:]' <"${REPO_ROOT}/.codeql-version")
	if [[ "${dry_run}" == true ]]; then
		echo "  [DRY RUN] .codeql-version: ${old} -> v${base_version}"
	else
		printf "v%s\n" "${base_version}" >"${REPO_ROOT}/.codeql-version"
		echo "  ✅ .codeql-version: ${old} -> v${base_version}"
	fi
	updated_count=$((updated_count + 1))

	while IFS= read -r manifest; do
		old=$(awk '/^version:/ {print $2; exit}' "${REPO_ROOT}/${manifest}")
		if [[ "${dry_run}" == true ]]; then
			echo "  [DRY RUN] ${manifest}: ${old} -> ${new_version}"
		else
			replace_once "${REPO_ROOT}/${manifest}" '^version:' "s/^version:[[:space:]]*.*/version: ${new_version}/"
			echo "  ✅ ${manifest}: ${old} -> ${new_version}"
		fi
		updated_count=$((updated_count + 1))
	done < <(cd "${REPO_ROOT}" && pack_manifests)

	old=$(read_printast_version)
	if [[ "${dry_run}" == true ]]; then
		echo "  [DRY RUN] ${PRINTAST_GO}: ${old} -> ${base_version}"
	else
		replace_once "${REPO_ROOT}/${PRINTAST_GO}" '^const ToolsPack = toolsPackName + "@' \
			"s/^const ToolsPack = toolsPackName + \"@[^\"]*\"/const ToolsPack = toolsPackName + \"@${base_version}\"/"
		echo "  ✅ ${PRINTAST_GO}: ${old} -> ${base_version}"
	fi
	updated_count=$((updated_count + 1))

	old=$(read_copilot_version)
	if [[ "${dry_run}" == true ]]; then
		echo "  [DRY RUN] ${COPILOT_GO}: ${old} -> ${base_version}"
	else
		replace_once "${REPO_ROOT}/${COPILOT_GO}" 'codeql-development-mcp-server@' \
			"s/codeql-development-mcp-server@[^\" ]*/codeql-development-mcp-server@${base_version}/"
		echo "  ✅ ${COPILOT_GO}: ${old} -> ${base_version}"
	fi
	updated_count=$((updated_count + 1))

	echo ""
	if [[ "${dry_run}" == true ]]; then
		echo "Would update ${updated_count} files. (Dry run — no files modified)"
	else
		echo "Updated ${updated_count} files to version ${new_version}."
		echo ""
		echo "Next steps:"
		echo "  1. Run './scripts/upgrade-packs.sh' to upgrade pack dependencies and lock files"
		echo "  2. Run 'make verify' to validate the changes"
	fi
}

CHECK_MODE=false
DRY_RUN=false
SKIP_CLI_VALIDATION=false
NEW_VERSION=""

while [[ $# -gt 0 ]]; do
	case $1 in
	--check)
		CHECK_MODE=true
		shift
		if [[ $# -gt 0 && ! "$1" =~ ^-- ]]; then
			NEW_VERSION="${1#v}"
			shift
		fi
		;;
	--dry-run)
		DRY_RUN=true
		shift
		;;
	--skip-cli-validation)
		SKIP_CLI_VALIDATION=true
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		echo "Error: Unknown option $1" >&2
		usage >&2
		exit 1
		;;
	*)
		NEW_VERSION="${1#v}"
		shift
		;;
	esac
done

cd "${REPO_ROOT}"

if [[ "${CHECK_MODE}" == true ]]; then
	if [[ -n "${NEW_VERSION}" ]]; then
		validate_version "${NEW_VERSION}"
	fi
	check_versions "${NEW_VERSION}"
	exit $?
fi

if [[ -z "${NEW_VERSION}" ]]; then
	echo "Error: No version specified" >&2
	echo "" >&2
	usage >&2
	exit 1
fi

validate_version "${NEW_VERSION}"

if [[ "${SKIP_CLI_VALIDATION}" == false ]]; then
	validate_cli_version "${NEW_VERSION%%-*}"
	echo ""
else
	echo "⚠️  CLI version validation skipped (--skip-cli-validation)"
	echo ""
fi

update_versions "${NEW_VERSION}" "${DRY_RUN}"
