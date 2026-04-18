#!/usr/bin/env bash
# ions DinD wrapper.
#
# The runner bind-mounts the workspace at /__w inside a job container.
# When a script inside that container runs `docker run -v /__w/foo:/x`,
# docker CLI forwards the path to the host daemon — but /__w doesn't
# exist on the host, so the daemon fails with exit 125.
#
# This wrapper rewrites any -v / --mount / -w argument whose source is
# /__w/... to the equivalent host path, then invokes the real docker
# binary.
#
# IONS_HOST_WORK points at the on-host directory that the runner bound
# to /__w; ions sets this env var when it auto-injects the job container.

set -euo pipefail

real_docker="${IONS_DOCKER_BIN:-/usr/bin/docker.real}"
container_work="/__w"
host_work="${IONS_HOST_WORK:-}"

translate() {
    local p="$1"
    if [ -z "$host_work" ]; then
        printf '%s' "$p"
        return
    fi
    case "$p" in
        "$container_work")            printf '%s' "$host_work" ;;
        "$container_work"/*)          printf '%s%s' "$host_work" "${p#$container_work}" ;;
        ./*|.)
            local abs
            abs="$(cd "$(pwd)" 2>/dev/null && printf '%s' "$PWD")/${p#./}"
            translate "$abs"
            ;;
        *)                            printf '%s' "$p" ;;
    esac
}

# Rewrite -v HOST:CONT[:OPTS] / --volume ... / --mount type=bind,source=...,...
args=()
i=0
orig=("$@")
while [ "$i" -lt "$#" ]; do
    a="${orig[$i]}"
    case "$a" in
        -v|--volume)
            next="${orig[$((i+1))]:-}"
            IFS=':' read -r src rest <<<"$next"
            src="$(translate "$src")"
            args+=("$a" "${src}${rest:+:$rest}")
            i=$((i+2))
            continue
            ;;
        -v=*|--volume=*)
            val="${a#*=}"
            IFS=':' read -r src rest <<<"$val"
            src="$(translate "$src")"
            args+=("${a%%=*}=${src}${rest:+:$rest}")
            i=$((i+1))
            continue
            ;;
        --mount)
            next="${orig[$((i+1))]:-}"
            # crude: translate source=...
            rewritten="$(echo "$next" | awk -v h="$host_work" -v c="$container_work" '
                BEGIN { FS=","; OFS="," }
                {
                    for (f=1; f<=NF; f++) {
                        if ($f ~ /^(source|src)=/) {
                            split($f, kv, "=")
                            v = kv[2]
                            if (v == c) v = h
                            else if (index(v, c "/") == 1) v = h substr(v, length(c)+1)
                            $f = kv[1] "=" v
                        }
                    }
                    print
                }')"
            args+=("$a" "$rewritten")
            i=$((i+2))
            continue
            ;;
        *)
            args+=("$a")
            i=$((i+1))
            ;;
    esac
done

exec "$real_docker" "${args[@]}"
