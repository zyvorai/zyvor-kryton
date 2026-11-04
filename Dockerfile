# Copyright 2026 Zyvor AI Labs · https://zyvor.dev
# SPDX-License-Identifier: Apache-2.0
# Pinned by digest so builds are reproducible; bump via .github/dependabot.yml
# (docker ecosystem) or by re-resolving the tag's current digest.
FROM golang:1.27.3@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go test ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/krytond ./cmd/krytond && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/krytonctl ./cmd/krytonctl

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/krytond /krytond
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/krytond"]
