# Companion case study: bridge-damage detection

> This was an independent applied-ML research track. It is not part of the b2d-geo ETL, database, REST, or MCP serving path.

The case study is included because it demonstrates a different production skill: choosing and evaluating a detector under class imbalance, licensing, hardware, latency, and deployment constraints.

## Problem

The working dataset contained 378,072 bridge-inspection images, 754,992 bounding boxes, and nine damage classes. Its long tail was severe: the largest class contained 254,510 boxes while the smallest contained 258.

An aggregate mAP alone could therefore hide whether an intervention helped the rare classes that motivated it.

## Model and evaluation policy

- Primary experiment: DEIMv2-M with a DINOv3-derived backbone, warm-started from a COCO checkpoint.
- Baseline: YOLO11m for internal comparison.
- Required reporting: mAP@[.50:.95] **and per-class AP**.
- Fixed validation set: interventions changed training composition without silently changing the evaluation target.
- Selection criteria: quality, license posture, latency, target hardware, training cost, and deployment constraints.

The experiment record classified RDD2022 as CC BY 4.0, DACL10K as CC BY-NC 4.0, and the compared Ultralytics implementation as AGPL-3.0. Those records informed what entered training and what remained evaluation-only or internal-comparison-only. They are not legal advice; upstream terms must be rechecked before reuse or deployment.

## Results

| Experiment | Validation mAP@[.50:.95] | Observation |
| --- | ---: | --- |
| DEIMv2-M baseline, 10 epochs | `0.161` | Strongest aggregate run in the recorded experiments |
| RDD merge + class-dependent replication | `0.151` | Small aggregate cost, strongly non-uniform class effects |
| YOLO11m internal baseline, 10 epochs | `0.0768` | Useful accuracy floor, not selected for deployment |

The second experiment improved Spalling AP by 64% and Pothole AP by 28%, while SteelDefect fell by 51% and PaintDamage by 35%.

## Decision

The intervention could not honestly be summarized as "oversampling worked" or "oversampling failed." Moderate replication helped some targets; extreme replication damaged other target classes, consistent with memorization of duplicated instances.

The resulting recommendation was class-aware model selection and a narrower ablation around the useful replication window—not a single aggregate winner.

## What is public here

This document preserves the problem framing, evaluation policy, dated results, and decision. Raw images, labels, checkpoints, full experiment logs, cloud identifiers, and detailed proprietary recipes are intentionally excluded.

