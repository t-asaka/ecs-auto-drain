NAME     := drain-container-instance

SRCS    := $(shell find . -type f -name '*.go')
LDFLAGS := -ldflags="-s -w -extldflags \"-static\""

bin/$(NAME): $(SRCS)
	go build -a -tags netgo -installsuffix netgo $(LDFLAGS) -o bin/$(NAME)

dep:
ifeq ($(shell command -v dep 2> /dev/null),)
	go get -u github.com/golang/dep/...
endif

deps: dep
	dep ensure

install:
	go install $(LDFLAGS)

clean:
	rm -rf bin
	rm -rf vendor/*
	rm -rf dist

dist:
	mkdir -p ./dist/drain_container_instance/bin
	cp index.js ./dist/drain_container_instance
	cp -r bin ./dist/drain_container_instance
	cp package.json ./dist/drain_container_instance
	cd ./dist && zip -r drain_container_instance.zip drain_container_instance/ && cd ..

release: dist
	aws s3 cp ./dist/drain_container_instance.zip s3://${BUCKET_NAME}/lambda_functions/
	aws lambda update-function-code \
		--function-name ${FUNCTION_NAME} \
		--s3-bucket ${BUCKET_NAME} \
		--s3-key lambda_functions/drain_container_instance.zip
	rm -fr dist

.PHONY: deps clean install dist release
