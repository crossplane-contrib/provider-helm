FROM gcr.io/distroless/static@sha256:15f8637509792e05b97d83451b0b6ccfa4abd548f997e406ac1e72c87fe1fa78

ARG TARGETOS
ARG TARGETARCH

# distroless does not allow root level folder creations, which helm tries to do.
ENV HELM_CACHE_HOME /tmp/helm-cache
ENV HELM_CONFIG_HOME /tmp/helm-config
ENV HELM_DATA_HOME /tmp/helm-data

ADD bin/$TARGETOS\_$TARGETARCH/provider /usr/local/bin/provider-helm

USER 65532
ENTRYPOINT ["provider-helm"]
