Vendored so this folder builds on its own (e.g. on a generation host):

- `doris-parquet-s3-gen/`: clone of git@github.com:wushilin/doris-parquet-s3-gen (master)
- `datagen/`: copy of the local `datagen` working tree it depends on (`path = "../datagen"`), which is not on GitHub

`generate.sh` builds `doris-parquet-s3-gen/target/release/doris-parquet-s3-gen` on first use.

Build packages (AlmaLinux / RHEL): `sudo dnf install -y openssl-devel pkgconf-pkg-config perl cmake gcc-c++ libcurl-devel cyrus-sasl-devel zlib-devel`
(`datagen` links librdkafka, which needs curl and sasl headers).
