//! Nesting a flat row by dotted field names.
//!
//! The row context is flat: every field, `id` or `customer.name`, is one
//! key. Record-shaped outputs (Avro, Protobuf, nested JSON) want
//! `customer.name` inside a `customer` record, so the tree below is built
//! once from the field names and walked per row.

use anyhow::{anyhow, bail, Result};

use crate::compile::{CompiledSpec, RowContext};
use crate::value::Value;

#[derive(Debug, Clone)]
pub enum Node {
    /// A generated field: the key in the row context.
    Leaf(String),
    /// A record assembled from dotted paths, in first-seen order.
    Record(Vec<(String, Node)>),
}

#[derive(Debug, Clone)]
pub struct RowTree {
    pub root: Vec<(String, Node)>,
    nested: bool,
}

impl RowTree {
    /// The tree of the spec's visible fields, in output order.
    pub fn from_spec(spec: &CompiledSpec) -> Result<Self> {
        Self::from_names(spec.output_order.iter().map(|&index| spec.fields[index].name.clone()))
    }

    /// Build from field names; a name and one of its prefixes both present
    /// (`customer` and `customer.name`) cannot both be placed, so that fails.
    pub fn from_names(names: impl IntoIterator<Item = String>) -> Result<Self> {
        let mut root: Vec<(String, Node)> = Vec::new();
        let mut nested = false;
        for name in names {
            let segments: Vec<&str> = name.split('.').collect();
            if segments.iter().any(|segment| segment.is_empty()) {
                bail!("field `{}` has an empty path segment", name);
            }
            nested |= segments.len() > 1;
            insert(&mut root, &segments, &name, &name)?;
        }
        Ok(Self { root, nested })
    }

    /// One flat leaf per name, no nesting; used where dots are just text.
    pub fn flat(names: impl IntoIterator<Item = String>) -> Self {
        Self {
            root: names.into_iter().map(|name| (name.clone(), Node::Leaf(name))).collect(),
            nested: false,
        }
    }

    /// Whether any field lives inside a record.
    pub fn is_nested(&self) -> bool {
        self.nested
    }

    /// Every leaf's full path, depth first.
    pub fn leaf_paths(&self) -> Vec<String> {
        fn walk(nodes: &[(String, Node)], out: &mut Vec<String>) {
            for (_, node) in nodes {
                match node {
                    Node::Leaf(name) => out.push(name.clone()),
                    Node::Record(children) => walk(children, out),
                }
            }
        }
        let mut out = Vec::new();
        walk(&self.root, &mut out);
        out
    }

    /// The row as one nested struct value.
    pub fn build(&self, ctx: &RowContext) -> Result<Value> {
        build_record(&self.root, ctx)
    }
}

fn insert(nodes: &mut Vec<(String, Node)>, segments: &[&str], full: &str, name: &str) -> Result<()> {
    let head = segments[0];
    let rest = &segments[1..];
    if let Some((_, existing)) = nodes.iter_mut().find(|(key, _)| key == head) {
        match (existing, rest.is_empty()) {
            (Node::Record(children), false) => insert(children, rest, full, name),
            (Node::Leaf(other), _) => bail!(
                "field `{}` conflicts with field `{}`: a path and its prefix cannot both be fields",
                name,
                other
            ),
            (Node::Record(_), true) => bail!(
                "field `{}` conflicts with the fields nested under it: a path and its prefix cannot both be fields",
                name
            ),
        }
    } else if rest.is_empty() {
        nodes.push((head.to_string(), Node::Leaf(full.to_string())));
        Ok(())
    } else {
        let mut children = Vec::new();
        insert(&mut children, rest, full, name)?;
        nodes.push((head.to_string(), Node::Record(children)));
        Ok(())
    }
}

fn build_record(nodes: &[(String, Node)], ctx: &RowContext) -> Result<Value> {
    let mut fields = Vec::with_capacity(nodes.len());
    for (key, node) in nodes {
        let value = match node {
            Node::Leaf(name) => ctx
                .get(name)
                .cloned()
                .ok_or_else(|| anyhow!("no generated value for field `{}`", name))?,
            Node::Record(children) => build_record(children, ctx)?,
        };
        fields.push((key.clone(), value));
    }
    Ok(Value::Struct(fields))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn nests_dotted_names_in_order() {
        let tree = RowTree::from_names(
            ["id", "customer.name", "age", "customer.email"].map(String::from),
        )
        .unwrap();
        assert!(tree.is_nested());
        assert_eq!(tree.leaf_paths(), ["id", "customer.name", "customer.email", "age"]);
        let mut ctx = RowContext::new();
        ctx.insert("id".into(), Value::I64(1));
        ctx.insert("customer.name".into(), Value::String("Ann".into()));
        ctx.insert("customer.email".into(), Value::String("a@x".into()));
        ctx.insert("age".into(), Value::I64(30));
        let built = tree.build(&ctx).unwrap();
        assert_eq!(
            built.to_json_string(),
            r#"{"id":1,"customer":{"name":"Ann","email":"a@x"},"age":30}"#
        );
    }

    #[test]
    fn rejects_a_path_and_its_prefix() {
        let error = RowTree::from_names(["customer", "customer.name"].map(String::from))
            .expect_err("prefix conflict");
        assert!(error.to_string().contains("prefix"), "{}", error);
        let error = RowTree::from_names(["customer.name", "customer"].map(String::from))
            .expect_err("prefix conflict the other way round");
        assert!(error.to_string().contains("prefix"), "{}", error);
    }
}
