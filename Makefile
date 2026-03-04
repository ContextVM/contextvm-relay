.PHONY: check major minor patch

# Get current version from latest git tag, default to 0.0.1
VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.1")

check:
	go vet ./...
	go fmt ./...
	go test ./...

update-deps: 
	go get -u ./...
	
major:
	@$(eval NEW_VERSION := $(shell echo $(VERSION) | awk -F. '{print $$1+1".0.0"}'))
	@git tag -a v$(NEW_VERSION) -m "Release v$(NEW_VERSION)"
	@git push origin v$(NEW_VERSION)
	@echo "Released v$(NEW_VERSION)"

minor:
	@$(eval NEW_VERSION := $(shell echo $(VERSION) | awk -F. '{print $$1"."$$2+1".0"}'))
	@git tag -a v$(NEW_VERSION) -m "Release v$(NEW_VERSION)"
	@git push origin v$(NEW_VERSION)
	@echo "Released v$(NEW_VERSION)"

patch:
	@$(eval NEW_VERSION := $(shell echo $(VERSION) | awk -F. '{print $$1"."$$2"."$$3+1}'))
	@git tag -a v$(NEW_VERSION) -m "Release v$(NEW_VERSION)"
	@git push origin v$(NEW_VERSION)
	@echo "Released v$(NEW_VERSION)"