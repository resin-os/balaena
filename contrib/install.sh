#!/bin/sh

set -e

tag="v17.12.0"
tag=$(echo "$tag" | sed 's|+|.|g')

machine=$(uname -m)

case "$machine" in
	"armv5"*)
		arch="armv5"
		;;
	"armv6"*)
		arch="armv6"
		;;
	"armv7"*)
		arch="armv7"
		;;
	"armv8"*)
		arch="aarch64"
		;;
	"aarch64"*)
		arch="aarch64"
		;;
	"i386")
		arch="i386"
		;;
	"i686")
		arch="i386"
		;;
	"x86_64")
		arch="x86_64"
		;;
	*)
		echo "Unknown machine type: $machine" >&2
		exit 1
esac

url="https://github.com/balena-os/balena-engine/releases/download/${tag}/balena-engine-${tag}-${arch}.tar.gz"

sudo=
if [[ $(id -u) -ne 0 ]]; then
	if [[ $(command -v sudo) ]]; then
		sudo='sudo -E'
	fi
	if [[ $(command -v su) ]]; then
		sudo='su -c'
	fi
	if [[ -z $sudo ]]; then
		cat >&2 <<-EOF
		Error: this installer needs the ability to run commands as root.
		We are unable to find either "sudo" or "su" available to make this happen.
		EOF
		exit 1
	fi
fi

# check for curl and tar
abort=0
for cmd in curl tar; do
	if [[ -z $(command -v $cmd) ]]; then
		cat >&2 <<-EOF
		Error: unable to find required command: $cmd
		EOF
		abort=1
	fi
done
[ $abort ] && exit 1

tarball="/usr/local/bin/balena-engine-${tag}-${arch}.tar.gz"
$sudo curl -sSL "$url" --continue-at "-" --output "$tarball"
$sudo tar xzfv "$tarball" -C /usr/local/bin --strip-components=1
$sudo rm -f "$tarball"

cat <<-EOF


   Installation successful!
     __          __                 ______            _
    / /_  ____ _/ /__  ____  ____ _/ ____/___  ____ _(_)___  ___
   / __ \\/ __ \`/ / _ \\/ __ \\/ __ \`/ __/ / __ \\/ __ \`/ / __ \\/ _ \\
  / /_/ / /_/ / /  __/ / / / /_/ / /___/ / / / /_/ / / / / /  __/
 /_.___/\\__,_/_/\\___/_/ /_/\\__,_/_____/_/ /_/\\__, /_/_/ /_/\\___/
                                            /____/
  the container engine for the IoT

To use balenaEngine you need to start balena-engine-daemon as a background process.
This can be done manually or using the init system scripts provided here:

    https://github.com/balena-os/balena-engine/tree/$tag/contrib/init

This requires adding a \"balena-engine\" group for the daemon to run under:

    sudo groupadd -r balena-engine

If you want to allow non-root users to run containers they can be added to this group
with something like:

    sudo usermod -aG balena-engine <user>

WARNING: Adding a user to the \"balena-engine\" group will grant the ability to run
         containers which can be used to obtain root privileges on the
         docker host.
         Refer to https://docs.docker.com/engine/security/security/#docker-daemon-attack-surface
         for more information.
EOF
