#include <filesystem>
#include <iostream>
#include <system_error>
#include <windows.h>
bool check_runtime(const char *name, const void *address, const char *expected_root)
{
    std::filesystem::path module_path;
#ifdef _WIN32
    HMODULE module = nullptr;
    if (!GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           reinterpret_cast<LPCWSTR>(address), &module))
        return false;
    wchar_t filename[32768] = {};
    const auto length = GetModuleFileNameW(module, filename, 32768);
    if (length == 0 || length == 32768)
        return false;
    module_path = filename;
#else
    Dl_info module = {};
    if (!dladdr(address, &module) || !module.dli_fname)
        return false;
    module_path = module.dli_fname;
#endif
    std::error_code error;
    const auto actual_path = std::filesystem::canonical(module_path, error);
    if (error)
        return false;
    const auto root = std::filesystem::canonical(expected_root, error);
    if (error)
        return false;
    std::cout << "runtime_" << name << '=' << actual_path.generic_u8string() << '\n';
    const auto relative = actual_path.lexically_relative(root);
    return !relative.empty() && !relative.is_absolute() && *relative.begin() != "..";
}
extern "C" int probe_function();
int main()
{
    if (probe_function() != 42) return 1;
    const auto root = std::filesystem::path(PROBE_ROOT).u8string();
    const bool direct = check_runtime("direct_address", reinterpret_cast<const void *>(&probe_function), root.c_str());
    const auto module = GetModuleHandleW(PROBE_DLL);
    const auto resolved = module ? GetProcAddress(module, "probe_function") : nullptr;
    const bool exported = resolved && check_runtime("exported_address", reinterpret_cast<const void *>(resolved), root.c_str());
    const bool wrong_root = resolved && check_runtime("wrong_root", reinterpret_cast<const void *>(resolved), std::filesystem::path(PROBE_CONSUMER_ROOT).u8string().c_str());
    std::cout << "direct=" << direct << " exported=" << exported << " wrong_root=" << wrong_root << '\n';
    return direct || !exported || wrong_root ? 2 : 0;
}
