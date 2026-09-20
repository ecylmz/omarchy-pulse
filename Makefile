.PHONY: locations test build

locations:
	python3 tools/gen-locations.py

test:
	cd server && go test ./...

build:
	cd server && go build -o pulse .
