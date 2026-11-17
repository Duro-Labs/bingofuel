PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLASMOID = frontends/plasma/package

.PHONY: build test test-go test-qml install install-core install-plasma uninstall preview

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bingofuel ./cmd/bingofuel

test: test-go test-qml

test-go:
	go vet ./...
	go test ./...

test-qml:
	QT_QPA_PLATFORM=offscreen /usr/lib/qt6/bin/qmltestrunner -input frontends/plasma/tests

install: install-core install-plasma

install-core: build
	install -Dm755 bingofuel $(PREFIX)/bin/bingofuel

install-plasma:
	kpackagetool6 -t Plasma/Applet -u $(PLASMOID) 2>/dev/null || kpackagetool6 -t Plasma/Applet -i $(PLASMOID)

uninstall:
	rm -f $(PREFIX)/bin/bingofuel
	kpackagetool6 -t Plasma/Applet -r com.fernandoduro.bingofuel || true

# Render the rings offscreen from live data: make preview OUT=/tmp/rings.png
OUT ?= /tmp/bingofuel-rings.png
preview: build
	./bingofuel fetch > /tmp/bingofuel-preview.json
	cd frontends/plasma/tools && QML_XHR_ALLOW_FILE_READ=1 QT_QPA_PLATFORM=offscreen /usr/lib/qt6/bin/qml preview.qml -- /tmp/bingofuel-preview.json $(OUT)
	@echo $(OUT)
