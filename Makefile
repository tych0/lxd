.PHONY: default
default:
	make -C lxc
	make -C lxd

.PHONY: check
check: default
	test -z "$(shell go fmt ./...)" || git diff --exit-code
	cd test && ./main.sh

.PHONY: clean
clean:
	-rm -f lxc/lxc lxd/lxd
