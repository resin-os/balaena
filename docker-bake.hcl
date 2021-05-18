variable "VERSION" {
	default = "0.0.0-unknown"
}
variable "BUILDTAGS" {
	default = "no_btrfs no_cri no_devmapper no_zfs exclude_disk_quota exclude_graphdriver_btrfs exclude_graphdriver_devicemapper exclude_graphdriver_zfs no_buildkit"
}

group "default" {
	targets = ["binary-amd64", "binary-aarch64", "binary-rpi"]
}

target "binary-amd64" {
	target = "cross"
	# output = [ "type=tar,dest=bundles/balena-engine-${VERSION}-amd64.tar" ]
	args = {
		VERSION = "${VERSION}"
		DOCKER_CROSSPLATFORMS = "linux/amd64"
		DOCKER_BUILDTAGS = "${BUILDTAGS}"
	}
}
target "binary-aarch64" {
	target = "cross"
	# output = [ "type=tar,dest=bundles/balena-engine-${VERSION}-aarch64.tar" ]
	args = {
		CROSS = "true"
		VERSION = "${VERSION}"
		DOCKER_CROSSPLATFORMS = "linux/arm64"
		DOCKER_BUILDTAGS = "${BUILDTAGS}"
	}
}
target "binary-rpi" {
	target = "cross"
	# output = [ "type=tar,dest=bundles/balena-engine-${VERSION}-rpi.tar" ]
	args = {
		CROSS = "rpi"
		VERSION = "${VERSION}"
		DOCKER_CROSSPLATFORMS = "linux/arm/v7"
		DOCKER_BUILDTAGS = "${BUILDTAGS}"
	}
}
