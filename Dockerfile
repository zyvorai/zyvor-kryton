# Copyright 2026 Zyvor AI Labs · https://zyvor.dev
# SPDX-License-Identifier: Apache-2.0
# Pinned by digest so builds are reproducible; bump via .github/dependabot.yml
# (docker ecosystem) or by re-resolving the tag's current digest.
FROM golang:1.27.1@sha256:162be5298a40ed317005c8339c6de4d10d3eef336d66dc8e9259b03ab9d3a6d2 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go test ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/krytond ./cmd/krytond && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/krytonctl ./cmd/krytonctl

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /out/krytond /krytond
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/krytond"]
