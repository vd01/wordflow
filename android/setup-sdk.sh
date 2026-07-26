#!/bin/bash
# WordFlow Android - SDK Setup Script
# Run this after installing JDK 17 and downloading the Android command-line tools.
#
# Prerequisites:
#   - JDK 17 installed (JAVA_HOME set)
#   - Android command-line tools zip downloaded

set -e

SDK_DIR="$HOME/android-sdk"
CMDLINE_TOOLS_ZIP="$HOME/Downloads/android-cmdline-tools.zip"
JAVA_HOME="${JAVA_HOME:-/c/Program Files/Microsoft/jdk-17.0.13.11-hotspot}"

echo "=== WordFlow Android SDK Setup ==="
echo "SDK_DIR: $SDK_DIR"
echo "JAVA_HOME: $JAVA_HOME"

# 1. Extract command-line tools
if [ -f "$CMDLINE_TOOLS_ZIP" ]; then
    echo "Extracting command-line tools..."
    mkdir -p "$SDK_DIR/cmdline-tools"
    cd "$SDK_DIR/cmdline-tools"
    unzip -o "$CMDLINE_TOOLS_ZIP"
    # The zip extracts to a 'cmdline-tools' subdirectory; rename to 'latest'
    if [ -d cmdline-tools ]; then
        mv cmdline-tools latest
    fi
    echo "Command-line tools extracted."
else
    echo "ERROR: $CMDLINE_TOOLS_ZIP not found."
    echo "Download from: https://developer.android.com/studio#command-tools"
    exit 1
fi

# 2. Set up environment
export JAVA_HOME
export ANDROID_HOME="$SDK_DIR"
export PATH="$SDK_DIR/cmdline-tools/latest/bin:$SDK_DIR/platform-tools:$PATH"

# 3. Accept licenses
echo "Accepting Android SDK licenses..."
yes | sdkmanager --licenses 2>/dev/null || true

# 4. Install required SDK components
echo "Installing SDK components..."
sdkmanager "platforms;android-34" \
           "build-tools;34.0.0" \
           "platform-tools"

echo ""
echo "=== Setup Complete ==="
echo "ANDROID_HOME=$SDK_DIR"
echo ""
echo "To build the app:"
echo "  cd android"
echo "  export ANDROID_HOME=$SDK_DIR"
echo "  export JAVA_HOME=$JAVA_HOME"
echo "  ./gradlew assembleDebug"
