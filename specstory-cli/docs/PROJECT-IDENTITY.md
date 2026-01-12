# Project Identity

On extension activation, if Auto-save is enabled, AFTER we create the `.specstory` directory (if it didn't already exist) create a `.specstory/.project.json` file to capture project identity ONLY if the file doesn't already exist.

The file will always have a `workspace_id` key, and will sometimes have a `git_id` key. If the file already exists, do check it for the presence of a `git_id` and ONLY if there isn't one, see if we can now give it one.

```json
{
  "workspace_id": "hash",
  "workspace_id_at": ISO-8601-timestamp,
  "git_id": "hash",
  "git_id_at": ISO-8601-timestamp,
}
```

`git_id` is a repeatable hash from the url for the remote called "origin" from the .git/config (if present). If it's not present, there is no `git_id` key. The same remote "origin" will has the same `git_id`.

GitHub HTTPS clone:

```
[remote "origin"]
	url = https://github.com/patterns-ai-core/langchainrb.git
```

GitHub SSH clone:

```
[remote "origin"]
	url = git@github.com:patterns-ai-core/langchainrb.git
```

Other service clone (made up example):

```
[remote "origin"]
	url = git@gitlab.com:patterns-ai-core/langchainrb.git
```

We want these first 2 GitHub cases to give us the same hash, no matter if the user happens to clone via HTTPS or SSH, so look for and remove `https://github.com/` and `git@github.com:` if they are present in the URL before using it as a hash. If neither of those strings is present, like in the 3rd example, just use the whole URL as the hash that creates the `git_id`.

`workspace_id` is a repeatable hash from the full path of the cwd that the SpecStory CLI was run from. The same cwd will hash to the same `worskpace_id`.

## Reason for these IDs

We are looking for "project persistence" that's resiliant.

ID saves us from user moving/renaming the project directory. That's OK cause the `workspace_id` stays the same, it's been persisted to the `.specstory/.project.json` file.

The git ID provides a potentially even more resiliant ID, in the case of:
- User remove .specstory/
- With Git User can move/rename the workspace and then remove .specstory (with the same git origin)
- User has the same project in multiple devices, or places on the same device
- Multiple users have the same project on their devices

Despite our attempts here, the user is still SOL in the case of:
- Without Git User can move/rename/modify the workspace and then remove .specstory
- With Git User can move/rename/modify the workspace and then change the git origin and remove .specstory
(in the future for these SOL cases, we can use interaction with the user to reidentity the project)
