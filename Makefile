VERSION := $(shell git describe --tags --always --dirty)
LDFLAGS := -s -w -X github.com/weka/tatool-go/cmd.Version=$(VERSION)

.PHONY: all release clean

all: tatool

tatool:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o tatool .

release:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o tatool-linux-amd64 .

clean:
	rm -f tatool tatool-linux-*
