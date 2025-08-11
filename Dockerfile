# =========================
# Stage 1: Build Go app
# =========================
FROM golang:1.24-bookworm as build

# Install TensorFlow C API with error checking
RUN apt-get clean && apt-get update -y --fix-missing && \
  apt-get install -y --no-install-recommends curl unzip file && \
  # Download with error checking and retries
  curl -L --fail --retry 3 --retry-connrefused --retry-delay 1 \
  https://storage.googleapis.com/tensorflow/libtensorflow/libtensorflow-cpu-linux-x86_64-2.15.0.tar.gz \
  -o /tmp/libtensorflow.tar.gz && \
  # Verify it's a valid gzip file before extracting
  echo "Verifying downloaded file..." && \
  file /tmp/libtensorflow.tar.gz && \
  # Extract only if verification passes
  tar -C /usr/local -xzf /tmp/libtensorflow.tar.gz && \
  rm /tmp/libtensorflow.tar.gz && \
  ldconfig && \
  # Debug: List all installed TensorFlow files
  echo "=== TensorFlow files installed ===" && \
  find /usr/local -name "*tensorflow*" -type f && \
  echo "=== Library cache updated ===" && \
  ldconfig -p | grep tensorflow

WORKDIR /src
COPY go.mod go.sum ./
COPY .env .env
RUN ls -la /src/.env
RUN go mod download
COPY . .

# Build with debug info
RUN CGO_ENABLED=1 GOARCH=amd64 go build -o /bin/server . && \
  # Check what libraries the binary needs
  echo "=== Binary dependencies ===" && \
  ldd /bin/server | grep tensorflow || echo "No TensorFlow dependencies shown (statically linked?)"

# =========================
# Stage 2: Runtime minimal with debug
# =========================
FROM debian:bookworm-slim as final

# Install runtime dependencies - FIXED VERSION
RUN set -e && \
  apt-get clean && rm -rf /var/lib/apt/lists/* && \
  apt-get update -y --fix-missing && \
  apt-get install -y --no-install-recommends \
  ca-certificates \
  libgomp1 \
  chromium \
  file \
  webp \
  curl && \
  rm -rf /var/lib/apt/lists/* && \
  # Verify file command is installed
  echo "=== Verifying file command ===" && \
  which file && file --version

# Copy ALL TensorFlow files (lib and include directories)
COPY --from=build /usr/local/lib/ /usr/local/lib/
COPY --from=build /usr/local/include/ /usr/local/include/

# Update library cache with debug output
RUN echo "=== Copied TensorFlow files ===" && \
  find /usr/local -name "*tensorflow*" -type f && \
  echo "=== Updating library cache ===" && \
  ldconfig && \
  echo "=== Library cache contents ===" && \
  ldconfig -p | grep tensorflow && \
  echo "=== LD_LIBRARY_PATH ===" && \
  echo $LD_LIBRARY_PATH

# Copy binary
COPY --from=build /src/.env .env
COPY --from=build /bin/server /bin/server

# Set environment variables
ENV LD_LIBRARY_PATH="/usr/local/lib:${LD_LIBRARY_PATH}"
ENV PATH="/usr/local/bin:${PATH}"

# Debug the binary before running
RUN echo "=== Final binary check ===" && \
  ldd /bin/server | head -20 && \
  echo "=== TensorFlow library check ===" && \
  (ldd /bin/server | grep tensorflow || echo "TensorFlow libraries not showing in ldd") && \
  echo "=== File command check ===" && \
  which file && \
  echo "=== Testing file command ===" && \
  echo "test" > /tmp/test.txt && \
  file /tmp/test.txt && \
  rm /tmp/test.txt

EXPOSE 8080
CMD ["/bin/server"]
