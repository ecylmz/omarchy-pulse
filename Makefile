.PHONY: locations test build

locations:
	python3 tools/gen-locations.py

test:
	cd server && go test ./...
	cd plugin && node tests/model.test.js

build:
	cd server && go build -o pulse .
