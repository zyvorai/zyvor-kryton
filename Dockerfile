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

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/krytond /krytond
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/krytond"]
