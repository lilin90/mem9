.PHONY: build vet clean run test check-schema

build:
	cd server && go build -o mnemo-server ./cmd/mnemo-server

vet:
	cd server && go vet ./...


test:
	cd server && go test -race -count=1 ./...
clean:
	rm -f server/mnemo-server

run: build
	cd server && MNEMO_DSN="$(MNEMO_DSN)" ./mnemo-server

docker:
	docker build -t mnemo-server ./server

check-schema:
	@scripts/check-schema.sh
