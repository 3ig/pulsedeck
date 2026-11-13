# Release files carry no version in their names, so the README can link to
# https://github.com/3ig/pulsedeck/releases/latest/download/<file> forever.
VERSION ?= $(shell git describe --tags --always 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
JAVA17  ?= /opt/homebrew/opt/openjdk@17

.PHONY: run build app dist installer packages apk clean

run:            ## run the server on this machine
	go run .

build:          ## server binary for this machine
	go build -ldflags "$(LDFLAGS)" -o dist/pulsedeck .

app:            ## macOS: Pulsedeck.app + .dmg (menu-bar app; build on a Mac)
	packaging/macos/build-app.sh $(VERSION)

# Linux and Windows cross-compile from anywhere without a C toolchain. The
# Windows build is a GUI program so double-clicking it opens no console window.
dist:           ## Linux and Windows server binaries
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/pulsedeck-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/pulsedeck-linux-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS) -H=windowsgui" -o dist/pulsedeck-windows.exe .

installer: dist ## Windows setup program (needs makensis)
	makensis -V2 -DVERSION=$(VERSION) -DEXE=../../dist/pulsedeck-windows.exe -DOUT=../../dist/Pulsedeck-windows-setup.exe packaging/windows/installer.nsi

packages: dist  ## Linux .deb / .rpm / .tar.gz (needs nfpm)
	for arch in amd64 arm64; do \
	  tar -C dist -czf dist/pulsedeck-linux-$$arch.tar.gz --transform "s/pulsedeck-linux-$$arch/pulsedeck/" pulsedeck-linux-$$arch 2>/dev/null || \
	  tar -C dist -czf dist/pulsedeck-linux-$$arch.tar.gz -s "/pulsedeck-linux-$$arch/pulsedeck/" pulsedeck-linux-$$arch; \
	  for fmt in deb rpm; do \
	    sed -e "s/@ARCH@/$$arch/g" -e "s/@VERSION@/$(VERSION)/g" packaging/linux/nfpm.yaml > dist/nfpm-$$arch.yaml && \
	    nfpm package -f dist/nfpm-$$arch.yaml -p $$fmt -t dist/pulsedeck-linux-$$arch.$$fmt || exit 1; \
	  done; \
	  rm -f dist/nfpm-$$arch.yaml; \
	done

apk:            ## Android client (debug-signed unless a release keystore is configured)
	cd android && JAVA_HOME=$(JAVA17) ./gradlew assembleDebug -PappVersion=$(VERSION)
	cp android/app/build/outputs/apk/debug/app-debug.apk dist/Pulsedeck-android.apk

clean:
	rm -rf dist android/app/build android/build android/.gradle
