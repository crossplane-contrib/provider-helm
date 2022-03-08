FROM BASEIMAGE
RUN apk --no-cache add ca-certificates bash

ADD provider /usr/local/bin/crossplane-helm-provider

ENV XDG_CACHE_HOME /tmp
ENV XDG_CONFIG_HOME /tmp

EXPOSE 8080
USER 1001
ENTRYPOINT ["crossplane-helm-provider"]