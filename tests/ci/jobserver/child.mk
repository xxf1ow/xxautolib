.PHONY: run worker-a worker-b worker-c

$(info MAKEFLAGS=$(MAKEFLAGS))

run: worker-a worker-b worker-c

worker-a worker-b worker-c:
	@printf 'start %s\n' "$$(date +%s%N)" >> "$(LOG)"; sleep 0.2; printf 'end %s\n' "$$(date +%s%N)" >> "$(LOG)"
