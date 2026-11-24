VERSION ?= 0.1.0

.PHONY: test vet build-linux deb build-windows clean

vet:
	go vet ./...

test:
	go test -v -race ./...

build-linux:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/mic-relay ./cmd/mic-relay

deb: build-linux
	chmod +x ./packaging/deb/build-deb.sh
	./packaging/deb/build-deb.sh "$(VERSION)" dist/mic-relay dist/

build-windows:
	mkdir -p dist
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -trimpath -ldflags="-s -w -H=windowsgui" -o dist/mic-relay.exe ./cmd/mic-relay

clean:
	rm -rf dist
