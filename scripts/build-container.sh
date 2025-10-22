#!/bin/bash

# DeFuzz Container Setup
# Builds and runs a fuzzing environment with ARM support

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
IMAGE_NAME="defuzz-env"
TAG="latest"

echo "🚀 DeFuzz Fuzzing Environment Setup"
echo "==================================="

# Check if podman is available
if ! command -v podman &> /dev/null; then
    echo "❌ Error: Podman is not installed"
    echo "Install: sudo apt-get install podman"
    exit 1
fi

echo "📦 Building container image: $IMAGE_NAME:$TAG"

# Build the container
podman build \
    --network=host \
    -f "$PROJECT_ROOT/docker/Dockerfile.fuzzing" \
    -t "$IMAGE_NAME:$TAG" \
    "$PROJECT_ROOT/docker"

if [ $? -eq 0 ]; then
    echo "✅ Container built successfully!"
    echo ""
    
    # Test the container
    echo "🧪 Testing environment..."
    podman run --rm -v "$(pwd)/workspace:/workspace" "$IMAGE_NAME:$TAG" pwd

    
    # Ask if user wants to start interactive session
    read -p "Start interactive session now? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "🚀 Starting interactive session..."
        podman run -it --rm -v "$(pwd)/workspace:/workspace" "$IMAGE_NAME:$TAG"
    fi
else
    echo "❌ Build failed!"
    exit 1
fi
