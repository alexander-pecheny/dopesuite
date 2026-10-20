# The chgksuite CLI as an image with nothing else in it, for other projects to
# copy the binary out of:
#
#   FROM ghcr.io/alexander-pecheny/chgksuite:2026.09.24@sha256:… AS chgksuite
#   COPY --from=chgksuite /chgksuite /usr/local/bin/chgksuite
#
# The binaries are built beforehand by .github/workflows/chgksuite-image.yml
# (static, CGO_ENABLED=0), so this only picks the one for the target platform.
# pdftotext, which `credits` reads PDFs with, is the consumer's to install.
FROM scratch
ARG TARGETARCH
COPY chgksuite-linux-${TARGETARCH} /chgksuite
COPY LICENSE NOTICE /
ENTRYPOINT ["/chgksuite"]
