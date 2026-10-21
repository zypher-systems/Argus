FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# GO_TAGS=hosted builds the multi-org service. Leave it empty for a
# self-hosted image: only the hosted build honours ARGUS_HOSTED.
ARG GO_TAGS=
RUN CGO_ENABLED=0 go build -trimpath -tags "${GO_TAGS}" \
	-ldflags="-s -w" \
	-o /out/argus ./cmd/argus

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
	&& adduser -D -H -u 65532 argus
COPY --from=build /out/argus /usr/local/bin/argus
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["argus"]
CMD ["api"]
