#!/usr/bin/env python3
"""
Copyright 2018 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
"""

from contextlib import closing
from distutils.version import LooseVersion
import json
import os.path
import platform
import re
import shutil
import subprocess
import sys
import time

try:
    from urllib.request import urlopen
except ImportError:
    # Python 2.x compatibility hack.
    from urllib2 import urlopen

ONE_HOUR = 1 * 60 * 60

LATEST_PATTERN = re.compile(r"latest(-(?P<offset>\d+))?$")

LAST_GREEN_COMMIT_FILE = "https://storage.googleapis.com/bazel-untrusted-builds/last_green_commit/github.com/bazelbuild/bazel.git/bazel-bazel"

BAZEL_GCS_PATH_PATTERN = (
    "https://storage.googleapis.com/bazel-builds/artifacts/{platform}/{commit}/bazel"
)

SUPPORTED_PLATFORMS = {"linux": "ubuntu1404", "windows": "windows", "darwin": "macos"}


def decide_which_bazel_version_to_use():
    # Check in this order:
    # - env var "USE_BAZEL_VERSION" is set to a specific version.
    # - env var "USE_NIGHTLY_BAZEL" or "USE_BAZEL_NIGHTLY" is set -> latest
    #   nightly. (TODO)
    # - env var "USE_CANARY_BAZEL" or "USE_BAZEL_CANARY" is set -> latest
    #   rc. (TODO)
    # - the file workspace_root/tools/bazel exists -> that version. (TODO)
    # - workspace_root/.bazelversion exists -> read contents, that version.
    # - workspace_root/WORKSPACE contains a version -> that version. (TODO)
    # - fallback: latest release
    if "USE_BAZEL_VERSION" in os.environ:
        return os.environ["USE_BAZEL_VERSION"]

    workspace_root = find_workspace_root()
    if workspace_root:
        bazelversion_path = os.path.join(workspace_root, ".bazelversion")
        if os.path.exists(bazelversion_path):
            with open(bazelversion_path, "r") as f:
                return f.read().strip()

    return "latest"


def find_workspace_root(root=None):
    if root is None:
        root = os.getcwd()
    if os.path.exists(os.path.join(root, "WORKSPACE")):
        return root
    new_root = os.path.dirname(root)
    return find_workspace_root(new_root) if new_root != root else None


def resolve_version_label_to_number_or_commit(bazelisk_directory, version):
    if version == "last_green":
        return get_last_green_commit(), True

    if "latest" in version:
        match = LATEST_PATTERN.match(version)
        if not match:
            raise Exception(
                'Invalid version "{}". In addition to using a version '
                'number such as "0.20.0", you can use values such as '
                '"latest" and "latest-N", with N being a non-negative '
                "integer.".format(version)
            )

        history = get_version_history(bazelisk_directory)
        offset = int(match.group("offset") or "0")
        return resolve_latest_version(history, offset), False

    return version, False


def get_last_green_commit():
    return read_remote_file(LAST_GREEN_COMMIT_FILE).strip()


def get_releases_json(bazelisk_directory):
    """Returns the most recent versions of Bazel, in descending order."""
    releases = os.path.join(bazelisk_directory, "releases.json")

    # Use a cached version if it's fresh enough.
    if os.path.exists(releases):
        if abs(time.time() - os.path.getmtime(releases)) < ONE_HOUR:
            with open(releases, "rb") as f:
                try:
                    return json.loads(f.read().decode("utf-8"))
                except json.decoder.JSONDecodeError:
                    print("WARN: Could not parse cached releases.json.")
                    pass

    with open(releases, "wb") as f:
        body = read_remote_file("https://api.github.com/repos/bazelbuild/bazel/releases")
        f.write(body.encode("utf-8"))
        return json.loads(body)


def read_remote_file(url):
    with urlopen(url) as res:
        return res.read().decode(res.info().get_content_charset("iso-8859-1"))


def get_version_history(bazelisk_directory):
    ordered = sorted(
        (
            LooseVersion(release["tag_name"])
            for release in get_releases_json(bazelisk_directory)
            if not release["prerelease"]
        ),
        reverse=True,
    )
    return [str(v) for v in ordered]


def resolve_latest_version(version_history, offset):
    if offset >= len(version_history):
        version = "latest-{}".format(offset) if offset else "latest"
        raise Exception(
            'Cannot resolve version "{}": There are only {} Bazel '
            "releases.".format(version, len(version_history))
        )

    # This only works since we store the history in descending order.
    return version_history[offset]


def determine_bazel_filename(version):
    machine = normalized_machine_arch_name()
    if machine != "x86_64":
        raise Exception(
            'Unsupported machine architecture "{}". Bazel currently only supports x86_64.'.format(
                machine
            )
        )

    operating_system = platform.system().lower()
    if operating_system not in ("linux", "darwin", "windows"):
        raise Exception(
            'Unsupported operating system "{}". '
            "Bazel currently only supports Linux, macOS and Windows.".format(operating_system)
        )

    filename_ending = ".exe" if operating_system == "windows" else ""
    return "bazel-{}-{}-{}{}".format(version, operating_system, machine, filename_ending)


def normalized_machine_arch_name():
    machine = platform.machine().lower()
    if machine == "amd64":
        machine = "x86_64"
    return machine


def determine_url(version, is_commit, bazel_filename):
    if is_commit:
        sys.stderr.write("Using unreleased version at commit {}\n".format(version))
        # No need to validate the platform thanks to determine_bazel_filename().
        return BAZEL_GCS_PATH_PATTERN.format(
            platform=SUPPORTED_PLATFORMS[platform.system().lower()], commit=version
        )

    # Split version into base version and optional additional identifier.
    # Example: '0.19.1' -> ('0.19.1', None), '0.20.0rc1' -> ('0.20.0', 'rc1')
    (version, rc) = re.match(r"(\d*\.\d*(?:\.\d*)?)(rc\d)?", version).groups()
    return "https://releases.bazel.build/{}/{}/{}".format(
        version, rc if rc else "release", bazel_filename
    )


def download_bazel_into_directory(version, is_commit, directory):
    bazel_filename = determine_bazel_filename(version)
    url = determine_url(version, is_commit, bazel_filename)
    destination_path = os.path.join(directory, bazel_filename)
    if not os.path.exists(destination_path):
        sys.stderr.write("Downloading {}...\n".format(url))
        with closing(urlopen(url)) as response:
            with open(destination_path, "wb") as out_file:
                shutil.copyfileobj(response, out_file)
    os.chmod(destination_path, 0o755)
    return destination_path


def maybe_makedirs(path):
    """
  Creates a directory and its parents if necessary.
  """
    try:
        os.makedirs(path)
    except OSError as e:
        if not os.path.isdir(path):
            raise e


def execute_bazel(bazel_path, argv):
    # We cannot use close_fds on Windows, so disable it there.
    p = subprocess.Popen([bazel_path] + argv, close_fds=os.name != "nt")
    while True:
        try:
            return p.wait()
        except KeyboardInterrupt:
            # Bazel will also get the signal and terminate.
            # We should continue waiting until it does so.
            pass


def main(argv=None):
    if argv is None:
        argv = sys.argv

    bazelisk_directory = os.environ.get(
        "BAZELISK_HOME", os.path.join(os.path.expanduser("~"), ".bazelisk")
    )
    maybe_makedirs(bazelisk_directory)

    bazel_version = decide_which_bazel_version_to_use()
    bazel_version, is_commit = resolve_version_label_to_number_or_commit(
        bazelisk_directory, bazel_version
    )

    bazel_directory = os.path.join(bazelisk_directory, "bin")
    maybe_makedirs(bazel_directory)
    bazel_path = download_bazel_into_directory(bazel_version, is_commit, bazel_directory)

    return execute_bazel(bazel_path, argv[1:])


if __name__ == "__main__":
    sys.exit(main())
