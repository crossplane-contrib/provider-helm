FROM gcr.io/distroless/static@sha256:4c2cd1dee442c9a99924a378c7c126fac8fef14771579ef567c3f2bdd7a5122a

ARG TARGETOS
ARG TARGETARCH

# distroless does not allow root level folder creations, which helm tries to do.
ENV HELM_CACHE_HOME /tmp/helm-cache
ENV HELM_CONFIG_HOME /tmp/helm-config
ENV HELM_DATA_HOME /tmp/helm-data

ADD bin/$TARGETOS\_$TARGETARCH/provider /usr/local/bin/provider-helm

USER 65532
ENTRYPOINT ["provider-helm"]
