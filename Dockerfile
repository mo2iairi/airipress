FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /airipress ./cmd/airipress
FROM node:22-alpine
RUN apk add --no-cache ca-certificates git hugo py3-pillow python3 && mkdir -p /data /opt/airipress
COPY --from=build /airipress /usr/local/bin/airipress
COPY tools /opt/airipress/tools
ENV AIRIPRESS_TOOLS_DIR=/opt/airipress/tools
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/airipress"]
