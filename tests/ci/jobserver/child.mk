.PHONY: run worker-a worker-b worker-c

$(info MAKEFLAGS=$(MAKEFLAGS))

run: worker-a worker-b worker-c

worker-a worker-b worker-c:
	@"$(CMAKE_COMMAND)" "-DLOG=$(LOG)" -P worker.cmake
