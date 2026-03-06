.PHONY: build install clean deb deploy-deb

VERSION ?= 0.1.0

# Build for Linux (amd64)
build:
	GOOS=linux GOARCH=amd64 go build -o bin/bbsit ./cmd/bbsit
	GOOS=linux GOARCH=amd64 go build -o bin/bbsit-ctl ./cmd/bbsit-ctl

# Install to /opt/bbsit on local machine
install: build
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

# Build .deb package
deb: build
	$(eval PKG := dist/bbsit_$(VERSION)_amd64)
	rm -rf $(PKG)
	mkdir -p $(PKG)/DEBIAN
	mkdir -p $(PKG)/opt/bbsit/templates
	mkdir -p $(PKG)/opt/bbsit/static
	mkdir -p $(PKG)/opt/stacks
	mkdir -p $(PKG)/usr/local/bin
	mkdir -p $(PKG)/lib/systemd/system
	cp bin/bbsit $(PKG)/opt/bbsit/bbsit
	cp bin/bbsit-ctl $(PKG)/usr/local/bin/bbsit-ctl
	cp -r templates/. $(PKG)/opt/bbsit/templates/
	cp -r static/. $(PKG)/opt/bbsit/static/
	cp deploy/config.yaml $(PKG)/opt/bbsit/config.yaml
	cp deploy/bbsit.service $(PKG)/lib/systemd/system/bbsit.service
	sed -e 's/{{VERSION}}/$(VERSION)/g' -e 's/{{ARCH}}/amd64/g' \
		deploy/DEBIAN/control > $(PKG)/DEBIAN/control
	cp deploy/DEBIAN/conffiles $(PKG)/DEBIAN/conffiles
	cp deploy/DEBIAN/postinst $(PKG)/DEBIAN/postinst
	cp deploy/DEBIAN/prerm    $(PKG)/DEBIAN/prerm
	chmod 755 $(PKG)/DEBIAN/postinst $(PKG)/DEBIAN/prerm
	chmod 755 $(PKG)/opt/bbsit/bbsit $(PKG)/usr/local/bin/bbsit-ctl
	dpkg-deb --build --root-owner-group $(PKG) dist/bbsit_$(VERSION)_amd64.deb
	@echo "Built: dist/bbsit_$(VERSION)_amd64.deb"

# Deploy .deb to remote host via scp (set TARGET_HOST env var)
deploy-deb: deb
	@if [ -z "$(TARGET_HOST)" ]; then echo "Set TARGET_HOST=user@ip"; exit 1; fi
	scp dist/bbsit_$(VERSION)_amd64.deb $(TARGET_HOST):/tmp/
	ssh $(TARGET_HOST) 'sudo dpkg -i /tmp/bbsit_$(VERSION)_amd64.deb'
	@echo "Deployed $(VERSION) to $(TARGET_HOST)"

clean:
	rm -rf bin/ dist/
