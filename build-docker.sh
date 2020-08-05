#!/bin/sh

set -e

[ -n "$HERE" ] && PROGRESS="--progress=plain" # detect ci
[ -z "$HERE" ] && HERE="$(dirname "$(readlink -f -- "$0")")"
export DOCKER_BUILDKIT=1

#     __            __
#    / /____  _____/ /_
#   / __/ _ \/ ___/ __/
#  / /_/  __(__  ) /_
#  \__/\___/____/\__/
#

export DOCKER_GRAPHDRIVER=overlay2
export TEST_SKIP_INTEGRATION_CLI=1
[ -z "$SKIP_TEST" ] && make test-unit test-integration

#      __          _ __    __
#     / /_  __  __(_) /___/ /
#    / __ \/ / / / / / __  /
#   / /_/ / /_/ / / / /_/ /
#  /_.___/\__,_/_/_/\__,_/
#

# install buildx
BUILDX_VERSION="0.5.1"
BUILDX_BIN="${HOME}/.docker/cli-plugins/docker-buildx"
if [ ! -e "${BUILDX_BIN}" ]; then
	curl -LO "https://github.com/docker/buildx/releases/download/v${BUILDX_VERSION}/buildx-v${BUILDX_VERSION}.linux-amd64"
	mkdir -vp "$(dirname "${BUILDX_BIN}")"
	mv -vf "buildx-v${BUILDX_VERSION}.linux-amd64" "${BUILDX_BIN}"
	chmod a+x "${BUILDX_BIN}"
fi

VERSION=$(cat VERSION)
export VERSION

BUNDLE_DIR="${HERE}/bundles"
if [ ! -e "${BUNDLE_DIR}" ]; then
	mkdir -p "${BUNDLE_DIR}"
fi
[ -z "$SKIP_CLEAN" ] && rm -rf "${BUNDLE_DIR:?}/*"

# shellcheck disable=2068
docker buildx bake ${PROGRESS} \
	--set=*.output=type=local,dest="${BUNDLE_DIR}" $@

mkdir -p "${HERE}/deploy"
pack() {
	version="v${VERSION:?}"
	[ -n "$1" ] && platform="-$(echo "$1" | sed 's/\//-/g')"
	[ -n "$2" ] && postfix="-${2}"
	packfile="balena-engine-${version}${postfix}${platform}.tar.gz"
	echo "* packing ${packfile}"
	cd "${BUNDLE_DIR}/cross/${1}" || exit 1
	md5sum "balena-engine-${VERSION}.md5"
	tar cvzf "${HERE}/deploy/${packfile}" .
	cd "${HERE}" || exit 1
}

if [ -z "$SKIP_PACK" ]; then
	pack linux/amd64
	pack linux/arm64
	pack linux/arm/v7
fi
