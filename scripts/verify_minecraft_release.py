#!/usr/bin/env python3
"""Verify public release metadata against a local artifact. Does not install, publish, or validate PCL."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
from urllib.parse import unquote, urlparse
import zipfile


def verify(manifest, artifact):
    data = json.loads(manifest.read_text(encoding="utf-8"))
    release = data.get("release")
    if data.get("status") != "published" or not isinstance(release, dict):
        raise ValueError("release is not published")
    for key in ("version", "url", "format", "minecraft", "loader", "serverCompatibility", "pcl", "publishedAt", "testedAt"):
        if not isinstance(release.get(key), str) or not release[key].strip():
            raise ValueError(f"missing release field: {key}")
    if release.get("verified") is not True:
        raise ValueError("PCL end-to-end validation must be recorded before publication")
    path = urlparse(release["url"])
    if not (path.scheme == "https" or (not path.scheme and release["url"].startswith("/"))):
        raise ValueError("download must use HTTPS or a site-relative URL")
    filename = unquote(PurePosixPath(path.path).name)
    if filename != artifact.name or release["version"] not in filename:
        raise ValueError("download filename and version do not match the artifact")
    if artifact.stat().st_size != release.get("bytes"):
        raise ValueError("file size mismatch")
    with artifact.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    if digest != release.get("sha256", "").lower():
        raise ValueError("SHA256 mismatch")
    if release["format"] not in ("mrpack", "curseforge"):
        raise ValueError("unsupported package format")
    with zipfile.ZipFile(artifact) as archive:
        for name in archive.namelist():
            p = PurePosixPath(name)
            if p.is_absolute() or ".." in p.parts or "\\" in name:
                raise ValueError("unsafe archive path")
            lower = name.lower()
            if any(part in {"logs", "crash-reports"} for part in p.parts) or any(secret in lower for secret in ("launcher_accounts", "accounts.json", "tokens.json", ".pem", ".key")):
                raise ValueError("potential private data in archive")
        if release["format"] == "mrpack":
            if artifact.suffix != ".mrpack":
                raise ValueError("mrpack artifact must keep the .mrpack extension")
            index = json.loads(archive.read("modrinth.index.json"))
            if index.get("formatVersion") != 1 or index.get("game") != "minecraft":
                raise ValueError("invalid Modrinth index")
            if index.get("versionId") != release["version"] or index.get("dependencies", {}).get("minecraft") != release["minecraft"]:
                raise ValueError("package and release versions differ")
            for dependency in index.get("files", []):
                path = PurePosixPath(dependency.get("path", ""))
                if not str(path) or path.is_absolute() or ".." in path.parts:
                    raise ValueError("unsafe dependency path")
                if not dependency.get("hashes", {}).get("sha512") or not dependency.get("hashes", {}).get("sha1"):
                    raise ValueError("dependency hashes missing")
                if not dependency.get("downloads") or any(urlparse(url).scheme != "https" for url in dependency["downloads"]):
                    raise ValueError("dependency downloads must use HTTPS")
        else:
            index = json.loads(archive.read("manifest.json"))
            if index.get("manifestType") != "minecraftModpack" or index.get("minecraft", {}).get("version") != release["minecraft"]:
                raise ValueError("invalid CurseForge manifest")
    return digest


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("artifact", type=Path)
    args = parser.parse_args()
    try:
        verify(args.manifest, args.artifact)
    except (ValueError, KeyError, OSError, zipfile.BadZipFile) as error:
        parser.exit(1, f"Release check failed: {error}\n")
    print("PASS: metadata, filename, size, SHA256 and archive structure. PCL/Windows/device compatibility and licenses still require recorded human verification.")
