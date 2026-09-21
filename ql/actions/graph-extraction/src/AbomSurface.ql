/**
 * @name ABOM workflow and job surface
 * @description Emits per-job trigger, permission and runner attributes used to weight the
 *              risk of an Actions Bill of Materials graph node.
 * @id abom/surface
 * @tags abom
 *       supply-chain
 *       abom-observation
 */

import actions
import abom.actions.Surface

from Job job, string subjectPath, string subjectJob, string name, string value
where surfaceFact(job, subjectPath, subjectJob, name, value)
select subjectPath, subjectJob, name, value
