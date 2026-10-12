#!/usr/bin/env bash
set -euo pipefail

## upgrade-packs.sh
## Upgrade CodeQL pack dependencies for packs in the codeql-abom repository.
## Resolves and pins the latest compatible version of every exactly pinned
## upstream dependency (codeql/<lang>-all, codeql/<lang>-queries), regenerates
## lock files, re-pins the `version:` inside each suite, and keeps the
## codeql/actions-all version that customql scaffolds in step with ql/actions/lib.
## This is the script to use when the CodeQL CLI version changes.
##
## Usage:
##   ./scripts/upgrade-packs.sh
##   ./scripts/upgrade-packs.sh --language actions
##   ./scripts/upgrade-packs.sh --repin

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

CUSTOMQL_GO="internal/customql/customql.go"

LANGUAGE=""
REPIN_ONLY=false

cd "${REPO_ROOT}"

VALID_LANGUAGES=()
while IFS= read -r lang; do
	VALID_LANGUAGES+=("${lang}")
done < <(find ql -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort)

usage() {
	cat <<EOF
Usage: $0 [OPTIONS]

Upgrade CodeQL pack dependencies for all packs in the repository.
Resolves and pins the latest compatible version of each exactly pinned
upstream dependency, then regenerates codeql-pack.lock.yml files and
re-pins the suites.

OPTIONS:
    --language <lang>  Upgrade packs only for the specified language
                       Valid values: ${VALID_LANGUAGES[*]}
    --repin            Only re-pin each suite's \`version:\` from the lock files
                       and check versions; does not run CodeQL. Honors --language.
    -h, --help         Show this help message

By default, the script upgrades packs for all languages.
EOF
}

while [[ $# -gt 0 ]]; do
	case $1 in
	--language)
		if [[ $# -lt 2 || "${2-}" == -* ]]; then
			echo "Error: --language requires a value" >&2
			usage >&2
			exit 1
		fi
		LANGUAGE="$2"
		shift 2
		;;
	--repin)
		REPIN_ONLY=true
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "Error: Unknown option $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

if [[ -n "${LANGUAGE}" ]]; then
	LANGUAGE_VALID=false
	for valid_lang in "${VALID_LANGUAGES[@]}"; do
		if [[ "${LANGUAGE}" == "${valid_lang}" ]]; then
			LANGUAGE_VALID=true
			break
		fi
	done
	if [[ "${LANGUAGE_VALID}" == false ]]; then
		echo "Error: Invalid language '${LANGUAGE}'" >&2
		echo "Valid languages: ${VALID_LANGUAGES[*]}" >&2
		exit 1
	fi
	LANG_GLOB="${LANGUAGE}"
else
	LANG_GLOB="*"
fi

## Packs skipped because a transitive dependency is not yet published to the
## registry (an upstream publishing gap outside this repository's control).
SKIPPED_PACKS=()

## Without a token the CLI reports an unpublished pack as "Could not create access credentials" (a 403).
is_unpublished_dependency_error() {
	local output="$1"
	echo "${output}" | grep -qiE "not found in the registry|Could not create access credentials"
}

## Replaces the first line starting `key:` with `key: value`, so a comment on that line is lost.
set_scalar() {
	local file=$1 key=$2 value=$3
	awk -v k="$key" -v v="$value" '
		!done && index($0, k ":") == 1 { print k ": " v; done = 1; next }
		{ print }
	' "$file" >"$file.tmp"
	mv "$file.tmp" "$file"
}

locked_version() {
	local lock="$1" dep="$2"
	awk -v p="${dep}:" '$1 == p {getline; print $2; exit}' "${lock}" 2>/dev/null
}

## Runs `codeql pack upgrade`, recording an unpublished-dependency failure as a skip.
## Returns 0 on success, 2 on skip, 1 on any other failure.
run_pack_upgrade() {
	local pack_dir="$1"
	local output
	if output=$(codeql pack upgrade -- "${pack_dir}" 2>&1 </dev/null); then
		return 0
	fi
	if is_unpublished_dependency_error "${output}"; then
		echo "  ⚠️  Skipping ${pack_dir}: a transitive dependency is not yet published to the registry." >&2
		echo "${output}" | sed 's/^/      /' >&2
		SKIPPED_PACKS+=("${pack_dir}")
		return 2
	fi
	echo "  ❌ codeql pack upgrade failed for ${pack_dir}:" >&2
	echo "${output}" >&2
	return 1
}

## Resolve and pin the latest compatible version of every exact upstream pin in a pack.
##
## An exact pin already satisfies its own constraint, so `codeql pack upgrade`
## is a no-op against it. Each exact pin is temporarily rewritten to '*', the
## upgrade refreshes the lock file, and the manifest is restored and pinned to
## what the lock file resolved. `${workspace}` and wildcard dependencies are
## left floating (wildcarding a workspace dep would send the CLI to the
## registry for an unpublished pack), but the lock file is still refreshed.
pin_upstream_deps() {
	local pack_dir="$1"
	local pack_yml="${pack_dir}/codeql-pack.yml"
	local lock_file="${pack_dir}/codeql-pack.lock.yml"
	local pinned dep status resolved was

	echo "INFO: Upgrading '${pack_dir}'..."

	pinned=$(awk '/^dependencies:/ {deps=1; next}
	              /^[^[:space:]]/ {deps=0}
	              deps && $1 ~ /:$/ && $2 ~ /^[0-9]+\./ {print substr($1, 1, length($1)-1)}
	             ' "${pack_yml}")

	if [[ -z "${pinned}" ]]; then
		status=0
		run_pack_upgrade "${pack_dir}" || status=$?
		if [[ "${status}" -eq 1 ]]; then
			return 1
		elif [[ "${status}" -eq 0 ]]; then
			echo "  ℹ️  no exact pins (lock file upgraded, pinning skipped)"
		fi
		return 0
	fi

	cp "${pack_yml}" "${pack_yml}.bak"
	for dep in ${pinned}; do
		set_scalar "${pack_yml}" "  ${dep}" '"*"'
	done

	status=0
	run_pack_upgrade "${pack_dir}" || status=$?
	## Restore first so a skipped or unresolved dependency keeps its last known-good pin.
	mv "${pack_yml}.bak" "${pack_yml}"
	if [[ "${status}" -eq 1 ]]; then
		return 1
	elif [[ "${status}" -eq 2 ]]; then
		return 0
	fi

	for dep in ${pinned}; do
		resolved=$(locked_version "${lock_file}" "${dep}")
		was=$(awk -v p="${dep}:" '$1 == p {print $2; exit}' "${pack_yml}")
		if [[ -z "${resolved}" ]]; then
			echo "  ⚠️  ${dep}: not found in ${lock_file}, kept ${was}" >&2
			continue
		fi
		set_scalar "${pack_yml}" "  ${dep}" "${resolved}"
		if [[ "${was}" == "${resolved}" ]]; then
			echo "  ✅ ${dep}: ${resolved} (already current)"
		else
			echo "  ✅ ${dep}: ${was} -> ${resolved}"
		fi
	done
}

## Libraries first, then src, then test: workspace dependents resolve through their pins.
pack_dirs() {
	local lang_glob="$1" manifest
	for manifest in ql/${lang_glob}/lib/codeql-pack.yml ql/${lang_glob}/*/src/codeql-pack.yml ql/${lang_glob}/*/test/codeql-pack.yml; do
		[[ -e "${manifest}" ]] && dirname "${manifest}"
	done
	return 0
}

## The pack customql scaffolds for operators pins the same codeql/actions-all as ql/actions/lib.
sync_customql_actions_all() {
	local lib_pin current matches
	lib_pin=$(awk '$1 == "codeql/actions-all:" {print $2; exit}' ql/actions/lib/codeql-pack.yml)
	if [[ -z "${lib_pin}" ]]; then
		echo "  ❌ could not read codeql/actions-all from ql/actions/lib/codeql-pack.yml" >&2
		return 1
	fi
	matches=$(grep -c '^const ActionsAll = "' "${CUSTOMQL_GO}" || true)
	if [[ "${matches}" -ne 1 ]]; then
		echo "  ❌ expected exactly one 'const ActionsAll = \"' in ${CUSTOMQL_GO}, found ${matches}" >&2
		return 1
	fi
	current=$(sed -n 's/^const ActionsAll = "\([^"]*\)".*/\1/p' "${CUSTOMQL_GO}")
	if [[ "${lib_pin}" == "${current}" ]]; then
		echo "  ✅ ${CUSTOMQL_GO}: ActionsAll ${current} (already current)"
		return 0
	fi
	sed -i.bak "s/^const ActionsAll = \"[^\"]*\"/const ActionsAll = \"${lib_pin}\"/" "${CUSTOMQL_GO}"
	rm -f "${CUSTOMQL_GO}.bak"
	echo "  ✅ ${CUSTOMQL_GO}: ActionsAll ${current} -> ${lib_pin}"
}

## `codeql pack upgrade` never looks inside a suite, so each suite's `version:` is
## re-pinned from its pack's lock file, and the manifest dependency follows it.
repin_suites() {
	local lang_glob="$1"
	local suite pack_dir lock dep version
	while IFS= read -r suite; do
		pack_dir="${suite}"
		while [[ "${pack_dir}" != "." && ! -f "${pack_dir}/codeql-pack.yml" ]]; do
			pack_dir=$(dirname "${pack_dir}")
		done
		lock="${pack_dir}/codeql-pack.lock.yml"
		[[ -f "${lock}" ]] || continue

		## `from:` names the pack; the `version:` on the next line is what needs re-pinning.
		awk -v lockfile="${lock}" '
			FILENAME == lockfile {
				if (pending != "" && $1 == "version:") { locked[pending] = $2 }
				pending = (NF == 1 && $1 ~ /:$/) ? substr($1, 1, length($1) - 1) : ""
				next
			}
			/^[[:space:]]*from:/ { from = $2 }
			/^[[:space:]]*version:/ && from != "" {
				if (from in locked) { sub(/version:.*/, "version: " locked[from]) }
				from = ""
			}
			{ print }
		' "${lock}" "${suite}" >"${suite}.tmp"
		mv "${suite}.tmp" "${suite}"

		while read -r dep version; do
			set_scalar "${pack_dir}/codeql-pack.yml" "  ${dep}" "${version}"
		done < <(awk '/^[[:space:]]*from:/ {f=$2; next}
		              /^[[:space:]]*version:/ && f != "" {print f, $2; f=""}' "${suite}")
	done < <(find ql -path "ql/${lang_glob}/*" -name '*.qls' | sort)
	./scripts/check-versions.sh
}

if [[ "${REPIN_ONLY}" == true ]]; then
	repin_suites "${LANG_GLOB}"
	exit 0
fi

if [[ -n "${LANGUAGE}" ]]; then
	echo "Upgrading packs for language: ${LANGUAGE}"
else
	echo "Upgrading packs for all languages..."
fi

PACK_DIRS=$(pack_dirs "${LANG_GLOB}")
if [[ -z "${PACK_DIRS}" ]]; then
	echo "Error: no CodeQL packs found under ql/${LANG_GLOB}" >&2
	exit 1
fi
while IFS= read -r pack_dir; do
	pin_upstream_deps "${pack_dir}"
done <<<"${PACK_DIRS}"

if [[ -z "${LANGUAGE}" || "${LANGUAGE}" == "actions" ]]; then
	echo ""
	echo "INFO: Syncing the codeql/actions-all version customql scaffolds..."
	sync_customql_actions_all
fi

echo ""
echo "INFO: Re-pinning suites from the lock files..."
repin_suites "${LANG_GLOB}"

echo ""
if [[ ${#SKIPPED_PACKS[@]} -gt 0 ]]; then
	echo "⚠️  CodeQL pack lock files upgraded, but the following packs were skipped"
	echo "    because a transitive dependency is not yet published to the registry."
	echo "    They were left on their current pinned versions and should be"
	echo "    re-upgraded once the missing dependency is available:"
	for skipped in "${SKIPPED_PACKS[@]}"; do
		echo "      - ${skipped}"
	done
	## Lets update-codeql.yml name a partial upgrade in the pull request.
	if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
		echo "skipped=${SKIPPED_PACKS[*]}" >>"${GITHUB_OUTPUT}"
	fi
else
	echo "✅ All CodeQL pack lock files upgraded successfully."
fi
