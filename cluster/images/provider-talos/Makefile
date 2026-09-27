# ====================================================================================
# Setup Project

include ../../../build/makelib/common.mk

# ====================================================================================
#  Options

include ../../../build/makelib/imagelight.mk

# ====================================================================================
# Targets

img.build:
	@$(INFO) docker build $(IMAGE)
	@$(MAKE) BUILD_ARGS="--load" img.build.shared
	@$(MAKE) img.unpack
	@$(OK) docker build $(IMAGE)

# A daemon on the containerd image store (colima, current Docker Desktop) does not
# unpack a loaded image of a foreign platform, so `docker history` fails with
# "snapshot ... does not exist", and so does `crossplane xpkg build
# --embed-runtime-image`, which reads the image config through that API. Creating
# (never starting) a container of the image's platform unpacks it. It is a no-op
# for the native platform, e.g. linux_amd64 on the amd64 CI runners.
img.unpack:
	@cid=$$(docker create --platform $(IMAGE_PLATFORMS) $(IMAGE)) || $(FAIL); \
		docker rm $$cid >/dev/null || $(FAIL)

img.publish:
	@$(INFO) Skipping image publish for $(IMAGE)
	@echo Publish is deferred to xpkg machinery
	@$(OK) Image publish skipped for $(IMAGE)

img.build.shared:
	@cp Dockerfile $(IMAGE_TEMP_DIR) || $(FAIL)
	@cp -r $(OUTPUT_DIR)/bin/ $(IMAGE_TEMP_DIR)/bin || $(FAIL)
	@docker buildx build $(BUILD_ARGS) \
		--platform $(IMAGE_PLATFORMS) \
		-t $(IMAGE) \
		$(IMAGE_TEMP_DIR) || $(FAIL)

img.promote:
	@$(INFO) Skipping image promotion from $(FROM_IMAGE) to $(TO_IMAGE)
	@echo Promote is deferred to xpkg machinery
	@$(OK) Image promotion skipped for $(FROM_IMAGE) to $(TO_IMAGE)
