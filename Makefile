.PHONY: default
default:
	go build ./...

.PHONY: check
check: default
	test -z "$(shell go fmt ./...)" || { git diff; false; }
	cd test && ./main.sh

.PHONY: clean
clean:
	-rm -f lxc/lxc lxd/lxd
