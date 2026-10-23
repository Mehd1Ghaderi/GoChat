#!/bin/sh
# Deploys GoChat on this server: loads the image piped in (the output of
# `docker save`, gzipped or not), applies the migrations and restarts the
# server and the web gateway with it. This script's directory holds
# docker-compose.yml and .env. If they do not come up healthy, the image that
# ran before comes back and the script fails.
#
#   docker save gochat:latest | gzip | ssh deploy@server sh /opt/apps/gochat/deploy.sh
#
# The CI workflow runs it for every push to main (see the README).
set -eu
cd "$(dirname "$0")"

compose() { docker compose --file docker-compose.yml "$@"; }

if [ ! -f .env ]; then
	echo "deploy: there is no .env in $PWD: create it from .env.template" >&2
	exit 1
fi

# The image the server runs now stays at hand as gochat:previous.
running=$(docker inspect --format '{{.Image}}' "$(compose ps --all --quiet server 2>/dev/null)" 2>/dev/null || true)
if [ -n "$running" ]; then
	docker image tag "$running" gochat:previous
fi
docker load

started() {
	# The stores keep running across deploys; only the app is replaced.
	compose up --detach --no-build --wait --wait-timeout 300 scylla minio || return 1
	compose run --rm --no-deps scylla-init >/dev/null || return 1
	compose run --rm --no-deps minio-init >/dev/null || return 1
	compose run --rm --no-deps migrate || return 1
	compose up --detach --no-build --no-deps --force-recreate --wait --wait-timeout 180 server || return 1
	compose up --detach --no-build --no-deps --force-recreate --wait --wait-timeout 120 web
}

if started; then
	# Images replaced by earlier deploys are not needed any more.
	docker image prune --force --filter label=org.opencontainers.image.title=gochat >/dev/null
	echo "deploy: gochat $(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' gochat:latest) is running"
	exit 0
fi

echo "deploy: gochat did not come up healthy. Its last logs:" >&2
compose logs --no-color --tail 100 server web >&2 || true
if [ -n "$running" ]; then
	docker image tag gochat:previous gochat:latest
	if compose up --detach --no-build --no-deps --force-recreate --wait --wait-timeout 180 server web; then
		echo "deploy: went back to the image that ran before" >&2
	else
		echo "deploy: the image that ran before did not come up either" >&2
	fi
fi
exit 1
