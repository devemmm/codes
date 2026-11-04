#!/bin/sh

set -eu

CHECKSUMS_FILE="build/checksums.txt"
BINARY_NAME="codes"
TARGETS="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 linux/armv6 linux/armv7 linux/386 windows/amd64"
VERSION_PKG="github.com/devemmm/codes/internal/config.OpengistVersion"

if [ "${VERSION+x}" = x ]; then
    DETECTED_VERSION=$VERSION
elif GIT_VERSION=$(git describe --tags 2>/dev/null); then
    DETECTED_VERSION=$GIT_VERSION
else
    echo "Error: VERSION is not set and could not retrieve version from git tags. Exiting..." >&2
    exit 1
fi

VERSION=${DETECTED_VERSION#v}

if [ -z "$VERSION" ]; then
    echo "Error: Version is empty. Exiting..." >&2
    exit 1
fi

mkdir -p build

# Start with a clean checksum file to prevent stale or duplicate entries.
: > "$CHECKSUMS_FILE"

for TARGET in $TARGETS; do
    GOOS=${TARGET%/*}
    GOARCH=${TARGET#*/}

    GOARM=""

    case "$GOOS-$GOARCH" in
        linux-armv6)
            GOARCH="arm"
            GOARM=6
            ;;
        linux-armv7)
            GOARCH="arm"
            GOARM=7
            ;;
    esac

    OUTPUT_PARENT_DIR="build/$GOOS-$GOARCH${GOARM:+v$GOARM}-$VERSION"
    OUTPUT_DIR="$OUTPUT_PARENT_DIR/$BINARY_NAME"
    OUTPUT_FILE="$OUTPUT_DIR/$BINARY_NAME"

    if [ "$GOOS" = "windows" ]; then
        OUTPUT_FILE="$OUTPUT_FILE.exe"
    fi

    ARCHIVE="build/$BINARY_NAME$VERSION-$GOOS-$GOARCH${GOARM:+v$GOARM}"

    echo "Building version $VERSION for $GOOS/$GOARCH${GOARM:+v$GOARM}..."

    mkdir -p "$OUTPUT_DIR"

    export GOOS GOARCH
    export CGO_ENABLED=0

    if [ -n "$GOARM" ]; then
        export GOARM
    else
        unset GOARM
    fi

    # Stop immediately if compilation fails.
    go build \
        -tags fs_embed \
        -ldflags "-X $VERSION_PKG=v$VERSION" \
        -o "$OUTPUT_FILE" .

    cp README.md LICENSE config.yml "$OUTPUT_DIR/"

    echo "Archiving for $GOOS/$GOARCH${GOARM:+v$GOARM}..."

    if [ "$GOOS" = "windows" ]; then
        ARCHIVE="$ARCHIVE.zip"

        (
            cd "$OUTPUT_PARENT_DIR"
            zip -qr "../$(basename "$ARCHIVE")" "$BINARY_NAME"
        )
    else
        ARCHIVE="$ARCHIVE.tar.gz"

        tar -czf "$ARCHIVE" \
            -C "$OUTPUT_PARENT_DIR" \
            "$BINARY_NAME"
    fi

    # Generate checksums on both macOS and Linux.
    if command -v sha256sum >/dev/null 2>&1; then
        CHECKSUM=$(sha256sum "$ARCHIVE")
    elif command -v shasum >/dev/null 2>&1; then
        CHECKSUM=$(shasum -a 256 "$ARCHIVE")
    else
        echo "Error: No SHA-256 utility found." >&2
        exit 1
    fi

    printf '%s\n' "$CHECKSUM" |
        awk '{name=$2; sub(/^build\//, "", name); print $1 "  " name}' \
        >> "$CHECKSUMS_FILE"

    echo "Successfully built: $ARCHIVE"
done

echo "Build and archiving complete."
echo "Checksums written to $CHECKSUMS_FILE"
