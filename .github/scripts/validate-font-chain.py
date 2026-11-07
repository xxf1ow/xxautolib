import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import urllib.request

root = Path(os.environ['GITHUB_WORKSPACE']).resolve()
work = Path(os.environ['WORK_ROOT']).resolve()
cmake = os.environ['CMAKE_COMMAND']
platform = os.environ['PLATFORM']
settings = json.loads((root / '.github/ci-tools.json').read_text())['production']
evidence = work / 'font-acceptance'
evidence.mkdir(parents=True, exist_ok=True)


def run(command, label, env=None, cwd=None):
    result = subprocess.run([str(arg) for arg in command], cwd=cwd or root,
                            env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (evidence / (label + '.log')).write_text(result.stdout)
    print(result.stdout, flush=True)
    result.check_returncode()


def configure_options():
    if platform.startswith('windows-'):
        config = settings['windows']
        return ['-G', config['generator'], '-A', config['platform'], '-T', config['toolset'],
                '-DCMAKE_MSVC_RUNTIME_LIBRARY=' + config['runtime']]
    options = ['-G', settings['darwin' if platform.startswith('darwin-') else 'linux']['generator']]
    if os.environ.get('MAKE_PROGRAM'):
        options.append('-DCMAKE_MAKE_PROGRAM=' + os.environ['MAKE_PROGRAM'])
    for key, env_key in [('CMAKE_C_COMPILER', 'CC'), ('CMAKE_CXX_COMPILER', 'CXX')]:
        if os.environ.get(env_key):
            options.append('-D' + key + '=' + os.environ[env_key])
    if platform.startswith('darwin-'):
        options += ['-DCMAKE_OSX_ARCHITECTURES=arm64', '-DCMAKE_OSX_DEPLOYMENT_TARGET=15.0']
        if os.environ.get('CMAKE_OSX_SYSROOT'):
            options.append('-DCMAKE_OSX_SYSROOT=' + os.environ['CMAKE_OSX_SYSROOT'])
    return options + ['-DCMAKE_BUILD_TYPE=Release', '-DCMAKE_POLICY_VERSION_MINIMUM=3.5']


def capabilities():
    prefix = work / 'install/freetype/2.13.2'
    options = (prefix / 'include/freetype2/freetype/config/ftoption.h').read_text()
    enabled = lambda name: bool(re.search(r'^\s*#\s*define\s+' + name + r'\b', options, re.M))
    assert enabled('FT_CONFIG_OPTION_SYSTEM_ZLIB'), 'External zlib was not enabled'
    assert not enabled('FT_CONFIG_OPTION_USE_HARFBUZZ'), 'FreeType must not use HarfBuzz'
    for name in ('PNG', 'BZIP2', 'BROTLI'):
        assert not enabled('FT_CONFIG_OPTION_USE_' + name), name + ' must be disabled'
    pc = (prefix / 'lib/pkgconfig/freetype2.pc').read_text()
    assert 'harfbuzz' not in pc.lower(), pc
    assert 'zlib' in pc, pc
    caches = []
    for file in (root / 'build').rglob('CMakeCache.txt'):
        cache = file.read_text(errors='replace')
        if not re.search(r'^CMAKE_PROJECT_NAME:STATIC=(freetype|OpenCV)$', cache, re.M):
            continue
        values = dict(re.findall(r'^([^/#\n][^:=\n]*):[^=\n]+=(.*)$', cache, re.M))
        if values['CMAKE_PROJECT_NAME'] == 'freetype':
            assert values.get('FT_DISABLE_HARFBUZZ') in ('ON', 'TRUE'), str(file)
            assert values.get('FT_REQUIRE_ZLIB') in ('ON', 'TRUE'), str(file)
        else:
            assert values.get('BUILD_ZLIB') in ('OFF', 'FALSE'), str(file)
        selected = [value for key, value in values.items() if key in ('ZLIB_LIBRARY', 'ZLIB_LIBRARY_RELEASE', 'ZLIB_LIBRARY_DEBUG') and
                    value and not value.endswith('-NOTFOUND') and value not in ('Release', 'Debug')]
        expected = (values['CMAKE_INSTALL_PREFIX'].replace('\\', '/').rsplit('/', 2)[0] + '/zlib/1.3.1').lower()
        assert selected, values
        for value in selected:
            for part in value.split(';'):
                if part in ('optimized', 'debug', 'general'):
                    continue
                assert part.replace('\\', '/').lower().startswith(expected + '/'), (file, part)
        caches.append({'path': str(file), 'zlib_libraries': selected})
    assert caches, 'Missing actual FreeType configure cache'
    result = {'freetype_uses_harfbuzz': False, 'freetype_uses_repository_zlib': True,
              'optional': {name: enabled('FT_CONFIG_OPTION_USE_' + name) for name in ('PNG', 'BZIP2', 'BROTLI')},
              'caches': caches, 'freetype_pkgconfig': pc}
    (evidence / 'capabilities.json').write_text(json.dumps(result, indent=2))
    print(json.dumps(result, indent=2))


def font_file():
    font = evidence / 'NotoSansCJKsc-Regular.otf'
    if not font.exists():
        url = ('https://raw.githubusercontent.com/notofonts/noto-cjk/'
               'f8d157532fbfaeda587e826d4cd5b21a49186f7c/'
               'Sans/OTF/SimplifiedChinese/NotoSansCJKsc-Regular.otf')
        with urllib.request.urlopen(url, timeout=120) as response:
            font.write_bytes(response.read())
    data = font.read_bytes()
    assert len(data) == 16437364
    assert hashlib.sha1(('blob %d\0' % len(data)).encode() + data).hexdigest() == 'dc15562470b4f842321894787a0d066879ccff8b'
    (evidence / 'font.json').write_text(json.dumps({'commit': 'f8d157532fbfaeda587e826d4cd5b21a49186f7c',
        'git_blob': 'dc15562470b4f842321894787a0d066879ccff8b', 'sha256': hashlib.sha256(data).hexdigest()}, indent=2))
    return font


def chinese(install, label):
    build = evidence / label
    env = dict(os.environ)
    for name in ('CMAKE_PREFIX_PATH', 'PKG_CONFIG_PATH', 'DYLD_LIBRARY_PATH', 'DYLD_FALLBACK_LIBRARY_PATH', 'LD_LIBRARY_PATH'):
        env.pop(name, None)
    run([cmake, '-S', root / 'tests/opencv/chinese', '-B', build,
         '-DAUTOLIB_ENTRY=' + str(install / 'autolib.cmake'),
         '-DCHINESE_FONT=' + str(font_file())] + configure_options(), label + '-configure', env)
    run([cmake, '--build', build, '--config', 'Release', '--verbose'], label + '-build', env)
    runtime = (build / 'runtime-dirs.txt').read_text()
    if runtime:
        env['PATH'] = runtime.replace(';', os.pathsep) + os.pathsep + env['PATH']
    ctest = Path(cmake).parent / ('ctest.exe' if platform.startswith('windows-') else 'ctest')
    run([ctest, '--test-dir', build, '-C', 'Release', '-V'], label + '-test', env)


def relocated():
    destination = work / 'font-relocated'
    transport = destination / 'transport'
    install = destination / 'install'
    transport.mkdir(parents=True)
    install.mkdir()
    run([cmake, '-E', 'tar', 'xf', work / 'transport.tar'], 'unpack-transport', cwd=transport)
    shutil.copy2(transport / 'autolib.cmake', install / 'autolib.cmake')
    manifest = json.loads((transport / 'transport.json').read_text())
    for item in manifest['assets']:
        archive = transport / item['path']
        assert hashlib.sha256(archive.read_bytes()).hexdigest() == item['sha256']
        run([cmake, '-E', 'tar', 'xf', archive], 'unpack-' + archive.name, cwd=install)
    for candidate in (transport / 'candidates').iterdir():
        shutil.copy2(candidate / 'package.toml', install / candidate.name / 'package.toml')
    original = work / 'install'
    hidden = work / 'original-install'
    original.rename(hidden)
    try:
        chinese(install, 'relocated')
    finally:
        hidden.rename(original)


if sys.argv[1] == 'direct':
    capabilities()
    chinese(work / 'install', 'direct')
elif sys.argv[1] == 'relocated':
    relocated()
else:
    raise SystemExit('Expected direct or relocated')
