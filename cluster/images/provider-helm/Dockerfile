FROM gcr.io/distroless/static@sha256:034df5c1737e8241f6ed6423a12e720ee0f6d6f088609ee172f98f6f2166ef5d

ARG TARGETOS
ARG TARGETARCH

# distroless does not allow root level folder creations, which helm tries to do.
ENV HELM_CACHE_HOME /tmp/helm-cache
ENV HELM_CONFIG_HOME /tmp/helm-config
ENV HELM_DATA_HOME /tmp/helm-data

ADD bin/$TARGETOS\_$TARGETARCH/provider /usr/local/bin/provider-helm

USER 65532
ENTRYPOINT ["provider-helm"]
