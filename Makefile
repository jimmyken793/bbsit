.PHONY: build build-arm64 install clean deb deb-arm64 deploy-deb deploy-deb-arm64

VERSION ?= 0.1.1

GO_SOURCES  := $(shell find . -name '*.go') go.mod go.sum
STATIC_FILES := $(wildcard templates/* static/*)
DEB_DEPS    := deploy/bbsit.service deploy/config.yaml \
               deploy/DEBIAN/control deploy/DEBIAN/conffiles \
               deploy/DEBIAN/postinst deploy/DEBIAN/prerm

# Phony aliases
build:       bin/bbsit bin/bbsit-ctl
build-arm64: bin/bbsit-arm64 bin/bbsit-ctl-arm64
deb:         dist/bbsit_$(VERSION)_amd64.deb dist/bbsit_$(VERSION)_arm64.deb

bin/ dist/:
	mkdir -p $@

# Binaries — rebuild only when Go sources change
bin/bbsit: $(GO_SOURCES) | bin/
	GOOS=linux GOARCH=amd64 go build -o $@ ./cmd/bbsit

bin/bbsit-ctl: $(GO_SOURCES) | bin/
	GOOS=linux GOARCH=amd64 go build -o $@ ./cmd/bbsit-ctl

bin/bbsit-arm64: $(GO_SOURCES) | bin/
	GOOS=linux GOARCH=arm64 go build -o $@ ./cmd/bbsit

bin/bbsit-ctl-arm64: $(GO_SOURCES) | bin/
	GOOS=linux GOARCH=arm64 go build -o $@ ./cmd/bbsit-ctl

# .deb packages — rebuild only when binaries, static files, or deploy config change
dist/bbsit_$(VERSION)_amd64.deb: bin/bbsit bin/bbsit-ctl $(STATIC_FILES) $(DEB_DEPS) | dist/
	$(eval PKG := $(@:.deb=))
	rm -rf $(PKG)
	mkdir -p $(PKG)/DEBIAN $(PKG)/opt/bbsit/templates $(PKG)/opt/bbsit/static \
	         $(PKG)/opt/stacks $(PKG)/usr/local/bin $(PKG)/lib/systemd/system
	cp bin/bbsit               $(PKG)/opt/bbsit/bbsit
	cp bin/bbsit-ctl           $(PKG)/usr/local/bin/bbsit-ctl
	cp -r templates/.          $(PKG)/opt/bbsit/templates/
	cp -r static/.             $(PKG)/opt/bbsit/static/
	cp deploy/config.yaml      $(PKG)/opt/bbsit/config.yaml
	cp deploy/bbsit.service    $(PKG)/lib/systemd/system/bbsit.service
	sed -e 's/{{VERSION}}/$(VERSION)/g' -e 's/{{ARCH}}/amd64/g' \
	    deploy/DEBIAN/control > $(PKG)/DEBIAN/control
	cp deploy/DEBIAN/conffiles $(PKG)/DEBIAN/conffiles
	cp deploy/DEBIAN/postinst  $(PKG)/DEBIAN/postinst
	cp deploy/DEBIAN/prerm     $(PKG)/DEBIAN/prerm
	chmod 755 $(PKG)/DEBIAN/postinst $(PKG)/DEBIAN/prerm
	chmod 755 $(PKG)/opt/bbsit/bbsit $(PKG)/usr/local/bin/bbsit-ctl
	dpkg-deb --build --root-owner-group $(PKG) $@
	@echo "Built: $@"

dist/bbsit_$(VERSION)_arm64.deb: bin/bbsit-arm64 bin/bbsit-ctl-arm64 $(STATIC_FILES) $(DEB_DEPS) | dist/
	$(eval PKG := $(@:.deb=))
	rm -rf $(PKG)
	mkdir -p $(PKG)/DEBIAN $(PKG)/opt/bbsit/templates $(PKG)/opt/bbsit/static \
	         $(PKG)/opt/stacks $(PKG)/usr/local/bin $(PKG)/lib/systemd/system
	cp bin/bbsit-arm64         $(PKG)/opt/bbsit/bbsit
	cp bin/bbsit-ctl-arm64     $(PKG)/usr/local/bin/bbsit-ctl
	cp -r templates/.          $(PKG)/opt/bbsit/templates/
	cp -r static/.             $(PKG)/opt/bbsit/static/
	cp deploy/config.yaml      $(PKG)/opt/bbsit/config.yaml
	cp deploy/bbsit.service    $(PKG)/lib/systemd/system/bbsit.service
	sed -e 's/{{VERSION}}/$(VERSION)/g' -e 's/{{ARCH}}/arm64/g' \
	    deploy/DEBIAN/control > $(PKG)/DEBIAN/control
	cp deploy/DEBIAN/conffiles $(PKG)/DEBIAN/conffiles
	cp deploy/DEBIAN/postinst  $(PKG)/DEBIAN/postinst
	cp deploy/DEBIAN/prerm     $(PKG)/DEBIAN/prerm
	chmod 755 $(PKG)/DEBIAN/postinst $(PKG)/DEBIAN/prerm
	chmod 755 $(PKG)/opt/bbsit/bbsit $(PKG)/usr/local/bin/bbsit-ctl
	dpkg-deb --build --root-owner-group $(PKG) $@
	@echo "Built: $@"

# Install to /opt/bbsit on local machine
install: bin/bbsit bin/bbsit-ctl
	sudo mkdir -p /opt/bbsit /opt/stacks
	sudo cp bin/bbsit /opt/bbsit/
	sudo cp bin/bbsit-ctl /usr/local/bin/
	sudo cp -r templates /opt/bbsit/
	sudo cp -r static /opt/bbsit/
	sudo cp deploy/config.yaml /opt/bbsit/config.yaml
	sudo cp deploy/bbsit.service /lib/systemd/system/
	sudo systemctl daemon-reload
	@echo ""
	@echo "Installed. Next steps:"
	@echo "  1. Edit /opt/bbsit/config.yaml"
	@echo "  2. sudo systemctl enable --now bbsit"
	@echo "  3. Open http://localhost:9090 to set password and add projects"

deploy-deb: dist/bbsit_$(VERSION)_amd64.deb
	@if [ -z "$(TARGET_HOST)" ]; then echo "Set TARGET_HOST=user@ip"; exit 1; fi
	scp $< $(TARGET_HOST):/tmp/
	ssh $(TARGET_HOST) 'sudo dpkg -i /tmp/$(notdir $<)'
	@echo "Deployed $(VERSION) to $(TARGET_HOST)"

deploy-deb-arm64: dist/bbsit_$(VERSION)_arm64.deb
	@if [ -z "$(TARGET_HOST)" ]; then echo "Set TARGET_HOST=user@ip"; exit 1; fi
	scp $< $(TARGET_HOST):/tmp/
	ssh $(TARGET_HOST) 'sudo dpkg -i /tmp/$(notdir $<)'
	@echo "Deployed $(VERSION) to $(TARGET_HOST)"

clean:
	rm -rf bin/ dist/
