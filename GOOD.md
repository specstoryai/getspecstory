
# GOOD: A companion for GIT

When AI agents are generating some, most, or all of your code, then occasional git commits of the resulting source code aren't sufficient. You also need a tool that ties the generated code back to the prompts and AI interactions that generated it.

Here’s a short technical explainer video of GOOD, a Git companion designed for this.


https://github.com/user-attachments/assets/ffc355ef-2ab7-45e1-9564-a350d0ea42c3


# How GOOD enhances GIT

| GOOD Capability | How It Complements Git |
| --- | --- |
| Tracks AI Responses | Each AI response is a versioned node, like a commit, but tied to a prompt rather than just a diff |
| Preserves Intent | Stores the natural language prompt that led to code generation, ensuring reproducibility and traceability |
| Links Human + AI Work | Anchors connect human edits to related AI suggestions and Git commits, offering bi-directional traceability |
| Maintains Dual Repositories | GOOD runs a "shadow Git" that mirrors the Git repo but with AI prompt-intent-output context |
| Improves Transparency | Developers can audit or revisit why code was generated, not just what changed |
| Boosts Collaboration | Others can reuse prompts or understand design decisions more easily across contributors |

# Call For Collaborators

## GOOD  
If you are working on a team that’s generating a lot of code with Cursor (or other AI IDEs), and struggling with understanding the intent behind all that code, we’d love some early users to work with us on alpha versions of GOOD. 

👉 [Drop us a line in this GitHub Discussion](https://github.com/specstoryai/getspecstory/discussions/51)

## Use Case Design Partners

If you're in charge of driving AI dev tool adoption at your company and are interested in ways to drive overall productivity we're actively seeking design partners for SpecStory for Teams.

👉 [Apply to be a Design Partner](https://specstory.com/teams)


# How GOOD works

GOOD mirrors the codebase, and the codebase's git repository with a "shadow" codebase and a "shadow" git repository. The difference is that the "shadow" git repository microversions the AI generated changes to files, adding data about the prompt that caused the change in the commit message.

## Scenario 1

In the video we walk through this scenario and its here for your reference:

Without git:

| Change | Prior      | File        | Line | GitHub Commit | Shadow Commit |
|--------|------------|-------------|------|---------------|---------------|
| AI     | New        | ai-1.txt    | 1    | No            | a0 a1         |
| AI     | AI         | ai-1.txt    | 2    | No            | a0 a1         |
| AI     | Manual     | mixed-1.txt | 2    | No            | a2            |
| Manual | New/Manual | mixed-1.txt | 1    | No            | a2            |
| Manual | AI         | mixed-1.txt | 3    | No            | a2            |

With git:

| Change | Prior  | File                    | Line | GitHub Commit | Shadow Commit |
|--------|--------|-------------------------|------|---------------|---------------|
| AI     | New    | ./with-git/ai-2.txt     | 1    | g0 g1         | a3 a4 a5      |
| AI     | AI     | ./with-git/ai-2.txt     | 2    | g0 g1         | a3 a4 a5      |
| AI     | Manual | ./with-git/mixed-2.txt  | 2    | g0 g1         | a6            |
| Manual | New    | ./with-git/manual-2.txt | 1    | g0 g1         |               |
| Manual | AI     | ./with-git/mixed-2.txt  | 3    | g0 g1         | a6            |
| Manual | Manual | ./with-git/manual-2.txt | 2    | g0 g1         |               |

## Match Validation

This section contains a markdown table of match checks and what origin they should report for each line checked. The "Lineage" and "Comment" columns are just for understanding, and are not used in the check.

| File                    | Line | Lineage                | Origin | Comments |
|-------------------------|------|------------------------|--------|----------|
| ai-1.txt                | 1    | a0                     | a0     | Case 0   |
| ai-1.txt                | 2    | a0 → a1                | a1     | Case 0   |
| mixed-1.txt             | 1    | m0                     | ?      | Case 2   |
| mixed-1.txt             | 2    | m0 → a2                | a2     | Case 0   |
| mixed-1.txt             | 3    | m0 → a2 → m1           | ?      | Case 2   |
| manual-1.txt            | 1    | m0                     | ?      | Case 6   |
| ./with-git/ai-2.txt     | 1    | a3 → g0 → g1           | a3     | Case 0   |
| ./with-git/ai-2.txt     | 2    | a3 → a4 → g0 → g1      | a4     | Case 0   |
| ./with-git/ai-2.txt     | 3    | a3 → g0 → a5 → g1      | a5     | Case 0   |
| ./with-git/mixed-2.txt  | 1    | m2 → g0 → g1           | g0     | Case 1   |
| ./with-git/mixed-2.txt  | 2    | m2 → g0 → a6 → g1      | a6     | Case 0   |
| ./with-git/mixed-2.txt  | 3    | m2 → g0 → a6 → m3 → g1 | g1     | Case 1   |
| ./with-git/manual-2.txt | 1    | m2 → g0 → g1           | g0     | Case 4   |
| ./with-git/manual-2.txt | 2    | m2 → g0 → m3 → g1      | g1     | Case 4   |
| ./with-git/manual-2.txt | 3    | m2 → g0 → g1 → m4      | ?      | Case 5   |

## Creation Sequence

This section contains the sequence of changes that constitute this scenario. Changes are either `a`I, `m`anual, or `g`it. And followed by a 0-based sequential number (e.g. g0, g1, g2).

Available actions for `a` and `m` are `create`, `modify`, `delete`. For `g` is `commit`.

The `create` action is followed by a `line`/`lines` parenthetical which indicates the lines created in the file, these can be either `1` or a range from `1-n`.

The `modify` action is followed by a `line`/`lines` parenthetical which indicates the lines modified in the file, these can be singular, comma delimitted, or a range.

**m0**
create manual-1.txt (line 1)
create mixed-1.txt (lines 1-3)

**a0**
create ai-1.txt (lines 1-2)

**a1**
modify ai-1.txt (line 2)

**a2**
modify mixed-1.txt (line 2, 3)

**m1**
modify mixed-1.txt (line 3)

**m2**
create ./with-git/manual-2.txt (lines 1-2)
create ./with-git/mixed-2.txt (lines 1-3)

**a3**
create ./with-git/ai-2.txt (lines 1-3)

**a4**
modify ./with-git/ai-2.txt (line 2)

**g0**
commit ./with-git/ai-2.txt
commit ./with-git/manual-2.txt
commit ./with-git/mixed-2.txt

**a5**
modify ./with-git/ai-2.txt (line 3)

**a6**
modify ./with-git/mixed-2.txt (line 2)

**m3**
modify ./with-git/mixed-2.txt (line 3)
modify ./with-git/manual-2.txt (line 2)

**g1**
commit ./with-git/ai-2.txt
commit ./with-git/mixed-2.txt
commit ./with-git/manual-2.txt

**m4**
modify ./with-git/manual-2.txt (line 3)



