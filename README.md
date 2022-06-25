# caddy-permissive-file-storage

This is a slight modification of Caddy's file storage plugin. The only difference is that files are stored with slightly more open permissions, so that other services can read Caddy's SSL certificates.

Use it by configuring it in your Caddyfile, e.g.

```
{
  storage permissive_file_storage {
    root "/tmp/caddy"
  }
  http_port 8080
  https_port 4443
}

localhost
```

This is used for certificate provisioning in [Shroud.email](https://shroud.email/).