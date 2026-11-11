#!/bin/sh
# Has Apple notarize FILE (a zip of the app, or a DMG) with the notarytool
# profile PROFILE, and waits for its answer. Exits 0 only when Apple says
# Accepted. Usage: macos/notarize.sh FILE PROFILE
#
# It does not use `notarytool submit --wait`: while Apple is slow (it was
# still working ten minutes in, on 2026-11-09) that wait gave up after the
# upload without printing any status, and release.sh read the silence as a
# rejection. So the file is submitted, and Apple is then asked about that
# submission until it answers Accepted, Invalid or Rejected, or
# CONCH_NOTARY_WAIT seconds (an hour) pass. CONCH_NOTARY_POLL is the
# seconds between asks; the tests make it 0.
set -eu

file=${1:?usage: macos/notarize.sh FILE PROFILE}
profile=${2:?usage: macos/notarize.sh FILE PROFILE}
poll=${CONCH_NOTARY_POLL:-30}
deadline=$(($(date +%s) + ${CONCH_NOTARY_WAIT:-3600}))

out=$(xcrun notarytool submit "$file" --keychain-profile "$profile" 2>&1) || true
echo "$out"
id=$(echo "$out" | sed -n 's/^ *id: //p' | head -1)
[ -n "$id" ] || { echo "notarize.sh: Apple gave no submission id for $file" >&2; exit 1; }

status=""
while :; do
	status=$(xcrun notarytool info "$id" --keychain-profile "$profile" 2>&1 | sed -n 's/^ *status: //p' | head -1) || true
	case "$status" in
	Accepted) echo "  status: Accepted"; exit 0 ;;
	Invalid | Rejected) break ;;
	esac
	[ "$(date +%s)" -lt "$deadline" ] || break
	sleep "$poll"
done
echo "notarize.sh: Apple did not accept $file (${status:-no answer}) — xcrun notarytool log $id --keychain-profile $profile says why" >&2
exit 1
