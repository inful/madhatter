FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Platform argument provided by buildx
ARG TARGETPLATFORM

# Copy the binary from goreleaser build context
COPY $TARGETPLATFORM/madhatter /usr/local/bin/madhatter

# Create a non-root user
RUN addgroup -S madhatter && adduser -S madhatter -G madhatter

# Switch to non-root user
USER madhatter

# Set working directory
WORKDIR /home/madhatter

# Expose default port (adjust if needed)
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/madhatter"]