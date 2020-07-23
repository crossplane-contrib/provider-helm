FROM BASEIMAGE
RUN apk --no-cache add ca-certificates bash

ADD provider /usr/local/bin/crossplane-helm-provider

COPY package /

EXPOSE 8080
USER 1001
ENTRYPOINT ["crossplane-helm-provider"]