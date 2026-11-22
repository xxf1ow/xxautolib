#ifndef _WIN32
#define _GNU_SOURCE
#include <dlfcn.h>
#else
#include <windows.h>
#endif

#include <stdio.h>
#include <string.h>
#include <zlib.h>

int main(int argc, char **argv)
{
    if (argc != 2 || strcmp(ZLIB_VERSION, argv[1]) || strcmp(zlibVersion(), argv[1])) {
        fprintf(stderr, "zlib version mismatch: headers=%s runtime=%s\n", ZLIB_VERSION, zlibVersion());
        return 1;
    }
#ifdef _WIN32
    char path[32768];
    HMODULE module = GetModuleHandleA("libz.dll");
    DWORD length = module ? GetModuleFileNameA(module, path, sizeof(path)) : 0;
    if (length == 0 || length >= sizeof(path)) {
        return 1;
    }
#else
    Dl_info info;
    if (!dladdr((const void *)zlibVersion, &info) || !info.dli_fname) {
        return 1;
    }
    const char *path = info.dli_fname;
#endif
    puts(path);
    return 0;
}
