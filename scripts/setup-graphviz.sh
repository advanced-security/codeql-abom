#!/usr/bin/env bash
#
# Installs Graphviz with the layout engines given, and proves each one draws.
#
#   setup-graphviz.sh [engine ...]   default: every engine internal/layout may run
#
# Every workflow installs Graphviz through this, so a pull request installs it exactly as a release
# does. Its packaging differs by runner image: Ubuntu 26.04 splits the layout engines out of
# graphviz, and Homebrew has no Intel macOS bottle. serve itself needs only dot; the layout tests
# run every engine.
set -euo pipefail

all="dot neato fdp sfdp twopi circo osage"
# shellcheck disable=SC2086 # split into one argument per engine.
[ $# -gt 0 ] || set -- $all

# Each engine is then run by name, so nothing outside internal/layout's list is.
for engine in "$@"; do
	case " $all " in
	*" $engine "*) ;;
	*)
		echo "setup-graphviz: $engine is not one of: $all" >&2
		exit 2
		;;
	esac
done

as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	else
		sudo "$@"
	fi
}

case "$(uname -s)/$(uname -m)" in
Linux/*)
	as_root apt-get update
	# From Ubuntu 26.04 every engine but dot is a separate package that graphviz does not even
	# recommend; before it, they ship inside graphviz's own library.
	plugin=$(apt-cache pkgnames libgvplugin-neato-layout |
		grep -E '^libgvplugin-neato-layout[0-9]+$' | sort -V | tail -n 1 || true)
	# shellcheck disable=SC2086 # $plugin is one package name or nothing.
	as_root apt-get install --yes --no-install-recommends graphviz $plugin
	;;
Darwin/arm64)
	brew install graphviz
	;;
Darwin/x86_64)
	# Homebrew builds no Intel bottle for Graphviz or anything it depends on, so this would compile
	# that whole tree from source. An amd64 build runs on arm64 under Rosetta instead.
	echo "setup-graphviz: Homebrew has no Intel macOS bottle for Graphviz; use an arm64 machine" >&2
	exit 1
	;;
*)
	echo "setup-graphviz: no Graphviz install for $(uname -s)/$(uname -m)" >&2
	exit 1
	;;
esac

# A missing engine is otherwise found minutes later, by a test, as a Graphviz error.
dot -V
missing=""
for engine in "$@"; do
	if ! echo 'digraph { subgraph cluster_g { a } a -> b }' | "$engine" -Tplain >/dev/null; then
		missing="$missing $engine"
	fi
done
if [ -n "$missing" ]; then
	echo "setup-graphviz: Graphviz cannot draw with:$missing" >&2
	exit 1
fi
echo "setup-graphviz: draws with $*"
