#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH='' cd -P "$(dirname "$0")" && pwd -P)
WRAPPER=$SCRIPT_DIR/local-agent-wrapper
WORK=$(mktemp -d "${TMPDIR:-/tmp}/local-agent-wrapper-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

# The smoke suite uses only synthetic credentials and must not inherit a
# developer or CI runner's provider configuration.
unset \
  XAI_API_KEY OPENAI_API_KEY OPENROUTER_API_KEY ANTHROPIC_API_KEY \
  LOCAL_AGENT_BIN LOCAL_AGENT_LOCAL_ONLY LOCAL_AGENT_NO_VAULT \
  LOCAL_AGENT_VAULT_KEYS LOCAL_AGENT_VAULT_PROJECT \
  LOCAL_AGENT_VAULT_REQUIRE_UNLOCKED LOCAL_AGENT_VAULT_VERBOSE \
  TVAULT_PASSPHRASE

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  haystack=$1
  needle=$2
  case $haystack in
    *"$needle"*) ;;
    *) fail "output is missing expected marker: $needle" ;;
  esac
}

assert_not_contains() {
  haystack=$1
  needle=$2
  case $haystack in
    *"$needle"*) fail "output contains forbidden marker: $needle" ;;
    *) ;;
  esac
}

mkdir -p "$WORK/bin" "$WORK/home/.local/libexec"

cat >"$WORK/real-local-agent" <<'EOF'
#!/bin/sh
printf 'argc=%s\n' "$#"
index=1
for arg do
  printf 'arg%s=<%s>\n' "$index" "$arg"
  index=$((index + 1))
done
if [ "${LOCAL_AGENT_LOCAL_ONLY+x}" = "x" ]; then
  printf 'local_only=<%s>\n' "$LOCAL_AGENT_LOCAL_ONLY"
else
  printf 'local_only=<unset>\n'
fi
if [ "${XAI_API_KEY+x}" = "x" ]; then
  printf 'xai=<present>\n'
else
  printf 'xai=<unset>\n'
fi
if [ "${OPENAI_API_KEY+x}" = "x" ]; then
  printf 'openai=<present>\n'
else
  printf 'openai=<unset>\n'
fi
EOF
chmod 755 "$WORK/real-local-agent"

# No TinyVault: the wrapper must be a transparent, argument-safe passthrough
# and must not weaken local-only policy.
bare_output=$(
  PATH=/usr/bin:/bin \
    LOCAL_AGENT_BIN=$WORK/real-local-agent \
    LOCAL_AGENT_NO_VAULT=0 \
    /bin/sh "$WRAPPER" "alpha beta" '*?[x]' ''
)
assert_contains "$bare_output" "argc=3"
assert_contains "$bare_output" "arg1=<alpha beta>"
assert_contains "$bare_output" "arg2=<*?[x]>"
assert_contains "$bare_output" "arg3=<>"
assert_contains "$bare_output" "local_only=<unset>"
assert_contains "$bare_output" "xai=<unset>"

# An explicit override is an authority boundary. Invalid or relative values
# must fail closed instead of silently selecting a different installation.
if PATH=/usr/bin:/bin LOCAL_AGENT_BIN=relative/local-agent \
  /bin/sh "$WRAPPER" >"$WORK/invalid.out" 2>"$WORK/invalid.err"; then
  fail "relative LOCAL_AGENT_BIN unexpectedly succeeded"
fi
assert_contains "$(cat "$WORK/invalid.err")" "must be an absolute executable path"

if PATH=/usr/bin:/bin LOCAL_AGENT_BIN=$WORK/missing-local-agent \
  /bin/sh "$WRAPPER" >"$WORK/missing.out" 2>"$WORK/missing.err"; then
  fail "missing LOCAL_AGENT_BIN unexpectedly succeeded"
fi
assert_contains "$(cat "$WORK/missing.err")" "is not an executable file"

# Release-archive installation: the wrapper must resolve the stable libexec
# location without relying on Go, Homebrew, or a second PATH entry.
install -m 755 "$WORK/real-local-agent" "$WORK/home/.local/libexec/local-agent"
libexec_output=$(
  HOME=$WORK/home \
    PATH=/usr/bin:/bin \
    LOCAL_AGENT_NO_VAULT=1 \
    /bin/sh "$WRAPPER" "from libexec"
)
assert_contains "$libexec_output" "arg1=<from libexec>"

# Fake TinyVault: expose only metadata to the assertion log. The fake injects a
# sentinel credential into the child, while neither the wrapper nor the child
# prints its value.
cat >"$WORK/bin/tvault" <<'EOF'
#!/bin/sh
set -eu
case ${1:-} in
  status)
    printf '%s\n' '{"initialized":true,"locked":false,"agent_running":false}'
    ;;
  list)
    printf '%s\n' XAI_API_KEY OTHER_SECRET
    ;;
  run)
    shift
    project=
    only=
    while [ "$#" -gt 0 ]; do
      case $1 in
        -p)
          project=$2
          shift 2
          ;;
        --only)
          only=$2
          shift 2
          ;;
        --)
          shift
          break
          ;;
        *)
          exit 64
          ;;
      esac
    done
    printf 'project=%s only=%s\n' "$project" "$only" >"$FAKE_TVAULT_AUDIT"
    [ "$only" = "XAI_API_KEY" ] || exit 65
    XAI_API_KEY=test-secret-must-not-appear
    export XAI_API_KEY
    exec "$@"
    ;;
  *)
    exit 64
    ;;
esac
EOF
chmod 755 "$WORK/bin/tvault"

inject_output=$(
  PATH=$WORK/bin:/usr/bin:/bin \
    LOCAL_AGENT_BIN=$WORK/real-local-agent \
    LOCAL_AGENT_VAULT_VERBOSE=1 \
    LOCAL_AGENT_VAULT_PROJECT=local-agent \
    FAKE_TVAULT_AUDIT=$WORK/tvault.audit \
    /bin/sh "$WRAPPER" "remote arg" 2>&1
)
assert_contains "$inject_output" "injecting 1 key(s)"
assert_contains "$inject_output" "arg1=<remote arg>"
assert_contains "$inject_output" "local_only=<false>"
assert_contains "$inject_output" "xai=<present>"
assert_contains "$(cat "$WORK/tvault.audit")" "project=local-agent only=XAI_API_KEY"
assert_not_contains "$inject_output" "test-secret-must-not-appear"

printf 'PASS: local-agent wrapper smoke tests\n'
