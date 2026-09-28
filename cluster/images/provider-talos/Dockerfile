# The provider is a single static Go binary. terraform-provider-talos runs
# in-process (upjet plugin-framework client), so there is no terraform CLI,
# no provider plugin mirror and no TF_* environment.
# The base is pinned by its multi-arch index digest (what buildx resolved the
# nonroot tag to for both linux/amd64 and linux/arm64 on 2026-09-28), so a moved
# tag cannot change the image; bump tag and digest together.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

ARG TARGETOS
ARG TARGETARCH

ADD "bin/${TARGETOS}_${TARGETARCH}/provider" /usr/local/bin/provider

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["provider"]
