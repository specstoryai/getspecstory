# SpecStory CLI Cloud Authentication

## Cloud API

The SpecStory Cloud API is hosted at `https://cloud.specstory.com/api/v1`.

Authenticated access to the API is provided by a "cloud access" (access) JWToken. This JWToken is trusted if valid, and is short lived (1 hour), and is used to authenticate requests to the API. The access JWToken is used to authenticate sync requests to the Cloud API.

The Cloud API trusts access JWTokens that are signed with the private key that is used to generate the access JWToken. The token is trusted if valid and not expired, with no additional validation required.

The CLI shouldn't make a request with an expired access JWToken, but if it does, the API will return a 401 Unauthorized response.

## Initial Cloud Auth Flow

The `specstory login` command is used to authenticate the CLI with the Cloud API.

The `specstory login` command opens the OS default browser to the `/cli-login` page (e.g. `https://cloud.specstory.com/cli-login`).

The CLI then blocks, waiting for the user to enter the 6-digit device code into the CLI.

If the user is logged in, or after the user logs in, the Cloud UI displays a 6-digit device code to the user in the browser. The user is then prompted to enter the device code into the CLI.

Once it gets the device code, the CLI then makes a POST request to the Cloud API at `/api/v1/device-login` with the 6-digit device code and some device metadata.

The POST parameters for the `/api/v1/device-login` request are:

- The 6-digit `device_code`
- The CLI's `hostname` (e.g. `seans-mbp.local`)
- The CLI's `os` (e.g. `darwin`)
- The CLI's `os_version` (e.g. `14.0.0`)
- The CLI's `os_display_name` (e.g. `macOS`)
- The CLI's `architecture` (e.g. `arm64`)
- The CLI's `username` (e.g. `sean`)

These parameters are used to identify the generated refresh JWToken for the CLI, which allows the user to manage their CLI login sessions from the Cloud UI.

If the device code is valid, the server returns a refresh JWToken and some metadata. The client stores these in the [cloud auth file](#cloud-auth-file) and uses the refresh token to get a new access JWToken with the [cloud access token refresh flow](#cloud-access-token-refresh-flow).

Example response:

```json
{
  "refreshToken": "eyJh...Mohw",
  "createdAt": "2025-08-09T14:15:52.460Z",
  "expiresAt": "2035-08-09T14:15:52.460Z",
  "user": {
    "email": "sean@specstory.com"
  }
}
```

Refresh tokens are long lived (10 years) but can be revoked by the user in the Cloud UI.

If the device code is invalid, the server returns a 401 Unauthorized response.

The server enforces that a device code can be used only once, and the code is valid for 24 hours. The 62^6 possible codes, and Cloudflare's DDOS protection, make it so a brute force attack will not be successful.

## Cloud Access Token Refresh Flow

An access JWToken is short lived (1 hour) and is used to authenticate requests to the API. If the token is expired, or the expiration is within 5 minutes, the client refreshes the access JWToken with a POST request to the Cloud API at `/api/v1/device-refresh` with the refresh JWToken in the `Authorization: Bearer <token>` header.

Example response:

```json
{
  "accessToken": "eyJh...Z3oc",
  "createdAt": "2025-08-09T14:15:52Z",
  "expiresAt": "2025-08-09T15:15:52Z"
}
```

The client should store the new returned access JWToken in the cloud auth file. The CLI then uses the access JWToken in the `Authorization: Bearer <token>` header to authenticate sync requests to the Cloud API.

If the refresh JWToken is expired or revoked, the server returns a 401 Unauthorized response, and the client should log out and an [initial cloud authentication flow](#initial-cloud-auth-flow) is required again.

## Cloud Auth File

The cloud auth file is stored in `~/.specstory/cli/auth.json` with 0600 permissions, and is used to store the cloud refresh and access tokens.

Example:

```json
{
  "cloud_refresh": {
    "token": "eyJh...Mohw",
    "as": "sean@specstory.com",
    "createdAt": "2025-08-07T14:32:49Z",
    "expiresAt": "2035-08-09T14:15:49Z",
    "lastValidAt": "2025-08-07T17:26:18Z"
  },
  "cloud_access": {
    "token": "eyJh...Z3oc",
    "updatedAt": "2025-08-08T14:15:52Z",
    "expiresAt": "2025-08-09T14:15:52Z"
  }
}
```

During the [initial cloud authentication flow](#initial-cloud-auth-flow), the CLI stores the refresh JWToken and the metadata returned from the Cloud API in the cloud auth file. The refresh JWToken is used to refresh the access JWToken before it expires. Each time the access JWToken is refreshed, the `lastValidAt` timestamp for the refresh JWToken is updated.

## Cloud Logout

The `specstory logout` command is used to remove the CLI's access to the Cloud API.

The `specstory logout` command makes a GET request to the Cloud API at `/api/v1/device-logout` with the refresh JWToken in the `Authorization: Bearer <token>` header.

The server returns a `200 OK` response if the logout is successful.

The client also removes the cloud auth file completely (no matter if the logout was successful or not).

The client is then logged out and an [initial cloud authentication flow](#initial-cloud-auth-flow) is required again.
