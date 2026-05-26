#!/bin/sh

set -eu

app_bundle="${ATTN_NATIVE_APP_BUNDLE:?ATTN_NATIVE_APP_BUNDLE is required}"

# The launched process monitors this wrapper PID. When Ctrl-C interrupts
# `make dev-native`, this shell exits and only the app created by this launch
# terminates; an existing profile owner never inherits this environment.
set -- -n -W --env "ATTN_NATIVE_LAUNCH_GUARD_PID=$$"
[ "${ATTN_PROFILE+x}" = x ] && set -- "$@" --env "ATTN_PROFILE=$ATTN_PROFILE"
[ "${ATTN_AUTOMATION+x}" = x ] && set -- "$@" --env "ATTN_AUTOMATION=$ATTN_AUTOMATION"
[ "${ATTN_AUTOMATION_BACKGROUND+x}" = x ] && set -- "$@" --env "ATTN_AUTOMATION_BACKGROUND=$ATTN_AUTOMATION_BACKGROUND"
[ "${ATTN_AUTOMATION_RESTORE_FOREGROUND_PID+x}" = x ] && set -- "$@" --env "ATTN_AUTOMATION_RESTORE_FOREGROUND_PID=$ATTN_AUTOMATION_RESTORE_FOREGROUND_PID"
[ "${ATTN_NATIVE_WS_URL+x}" = x ] && set -- "$@" --env "ATTN_NATIVE_WS_URL=$ATTN_NATIVE_WS_URL"

open "$@" "$app_bundle"
