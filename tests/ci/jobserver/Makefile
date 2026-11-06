.PHONY: all nested

all: nested

nested:
	+@$(MAKE) --no-print-directory -f child.mk run LOG="$(LOG)" CMAKE_COMMAND="$(CMAKE_COMMAND)"
